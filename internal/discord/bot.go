package discord

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/alfred-identity/web/internal/config"
	"github.com/alfred-identity/web/internal/store"
	"github.com/alfred-identity/web/internal/web"
	"github.com/bwmarrin/discordgo"
)

// Bot registers slash commands using Cfg.DiscordCommandPrefix (default alfred-identity-).
// Account/group management lives in the desktop GUI and web admin; Discord keeps SSO tokens.
// Guild role catalog + known users' Discord roles sync on a timer and on member events.
type Bot struct {
	Session *discordgo.Session
	Store   *store.Store
	Cfg     config.Config
	Log     *slog.Logger

	syncOnce sync.Once
	syncStop chan struct{}
	syncWG   sync.WaitGroup
	syncMu   sync.Mutex // serializes full role syncs
}

func New(cfg config.Config, st *store.Store, log *slog.Logger) (*Bot, error) {
	s, err := discordgo.New("Bot " + cfg.DiscordToken)
	if err != nil {
		return nil, err
	}
	s.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMembers
	b := &Bot{
		Session:  s,
		Store:    st,
		Cfg:      cfg,
		Log:      log,
		syncStop: make(chan struct{}),
	}
	s.AddHandler(b.onReady)
	s.AddHandler(b.onInteraction)
	s.AddHandler(b.onGuildMemberUpdate)
	s.AddHandler(b.onGuildMemberRemove)
	s.AddHandler(b.onGuildRoleCreate)
	s.AddHandler(b.onGuildRoleUpdate)
	s.AddHandler(b.onGuildRoleDelete)
	return b, nil
}

// cmd returns the full slash command name for a short key (e.g. "sso" → "alfred-identity-sso").
func (b *Bot) cmd(key string) string {
	return b.Cfg.DiscordCommandPrefix + key
}

// slash returns a user-facing "/prefixkey" mention for help text.
func (b *Bot) slash(key string) string {
	return "/" + b.cmd(key)
}

func (b *Bot) Open() error {
	return b.Session.Open()
}

func (b *Bot) Close() error {
	select {
	case <-b.syncStop:
	default:
		close(b.syncStop)
	}
	b.syncWG.Wait()
	return b.Session.Close()
}

func (b *Bot) onReady(s *discordgo.Session, r *discordgo.Ready) {
	b.Log.Info("discord ready", "user", r.User.Username, "guild_id", b.Cfg.DiscordGuildID, "command_prefix", b.Cfg.DiscordCommandPrefix)
	cmds := commandDefs(b.Cfg.DiscordCommandPrefix)
	if b.Cfg.DiscordGuildID == "" {
		b.Log.Warn("DISCORD_GUILD_ID empty; skipping slash command registration")
		return
	}
	// Bulk overwrite drops stale names when DISCORD_COMMAND_PREFIX changes or commands are removed.
	if _, err := s.ApplicationCommandBulkOverwrite(s.State.User.ID, b.Cfg.DiscordGuildID, cmds); err != nil {
		b.Log.Error("register commands", "err", err)
		return
	}
	for _, c := range cmds {
		b.Log.Info("register command ok", "name", c.Name)
	}
	b.syncOnce.Do(b.startRoleSyncLoop)
}

func (b *Bot) startRoleSyncLoop() {
	interval := b.Cfg.DiscordRoleSyncEvery
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	b.Log.Info("discord role sync loop", "interval", interval.String())
	b.syncWG.Add(1)
	go func() {
		defer b.syncWG.Done()
		b.syncAllRoles("startup")
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-b.syncStop:
				return
			case <-t.C:
				b.syncAllRoles("periodic")
			}
		}
	}()
}

func (b *Bot) syncAllRoles(reason string) {
	if b.Cfg.DiscordGuildID == "" || b.Session == nil {
		return
	}
	b.syncMu.Lock()
	defer b.syncMu.Unlock()
	b.syncGuildRoles(b.Session)
	b.syncMemberRoles(b.Session, reason)
}

func (b *Bot) syncGuildRoles(s *discordgo.Session) {
	if b.Cfg.DiscordGuildID == "" {
		return
	}
	roles, err := s.GuildRoles(b.Cfg.DiscordGuildID)
	if err != nil {
		b.Log.Warn("guild roles sync", "err", err)
		return
	}
	out := make([]store.DiscordRole, 0, len(roles))
	for _, r := range roles {
		if r == nil {
			continue
		}
		out = append(out, store.DiscordRole{ID: r.ID, Name: r.Name})
	}
	if err := b.Store.UpsertDiscordRoles(context.Background(), out); err != nil {
		b.Log.Warn("guild roles upsert", "err", err)
		return
	}
	b.Log.Info("guild roles synced", "count", len(out))
}

// syncMemberRoles refreshes role_ids_json for every known DB user from Discord guild membership.
func (b *Bot) syncMemberRoles(s *discordgo.Session, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	known, err := b.Store.ListUsersForRoleSync(ctx)
	if err != nil {
		b.Log.Warn("list users for role sync", "err", err)
		return
	}
	if len(known) == 0 {
		return
	}
	byDiscord := make(map[string]store.User, len(known))
	for _, u := range known {
		byDiscord[u.DiscordID] = u
	}

	updated := 0
	after := ""
	for {
		members, err := s.GuildMembers(b.Cfg.DiscordGuildID, after, 1000)
		if err != nil {
			b.Log.Warn("guild members sync", "err", err, "reason", reason)
			return
		}
		if len(members) == 0 {
			break
		}
		for _, m := range members {
			if m == nil || m.User == nil {
				continue
			}
			u, ok := byDiscord[m.User.ID]
			if !ok {
				continue
			}
			roles := append([]string(nil), m.Roles...)
			if err := b.Store.SetUserRoles(ctx, u.ID, roles); err != nil {
				b.Log.Warn("set user roles", "err", err, "discord_id", m.User.ID)
				continue
			}
			updated++
			delete(byDiscord, m.User.ID)
		}
		after = members[len(members)-1].User.ID
		if len(members) < 1000 {
			break
		}
	}

	cleared := 0
	for _, u := range byDiscord {
		if err := b.Store.SetUserRoles(ctx, u.ID, nil); err != nil {
			b.Log.Warn("clear roles for non-member", "err", err, "discord_id", u.DiscordID)
			continue
		}
		cleared++
	}
	b.Log.Info("member roles synced", "reason", reason, "updated", updated, "cleared", cleared, "known", len(known))
}

func (b *Bot) onGuildMemberUpdate(_ *discordgo.Session, e *discordgo.GuildMemberUpdate) {
	if e == nil || e.GuildID != b.Cfg.DiscordGuildID || e.User == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	u, err := b.Store.UserByDiscordID(ctx, e.User.ID)
	if err != nil {
		return // not a known SSO/web user
	}
	roles := append([]string(nil), e.Roles...)
	if err := b.Store.SetUserRoles(ctx, u.ID, roles); err != nil {
		b.Log.Warn("member update role sync", "err", err, "discord_id", e.User.ID)
		return
	}
	b.Log.Info("member roles updated", "discord_id", e.User.ID, "roles", len(roles))
}

func (b *Bot) onGuildMemberRemove(_ *discordgo.Session, e *discordgo.GuildMemberRemove) {
	if e == nil || e.GuildID != b.Cfg.DiscordGuildID || e.User == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	u, err := b.Store.UserByDiscordID(ctx, e.User.ID)
	if err != nil {
		return
	}
	if err := b.Store.SetUserRoles(ctx, u.ID, nil); err != nil {
		b.Log.Warn("member remove role clear", "err", err, "discord_id", e.User.ID)
		return
	}
	b.Log.Info("member roles cleared", "discord_id", e.User.ID)
}

func (b *Bot) onGuildRoleCreate(s *discordgo.Session, e *discordgo.GuildRoleCreate) {
	if e == nil || e.GuildID != b.Cfg.DiscordGuildID {
		return
	}
	b.syncGuildRoles(s)
}

func (b *Bot) onGuildRoleUpdate(s *discordgo.Session, e *discordgo.GuildRoleUpdate) {
	if e == nil || e.GuildID != b.Cfg.DiscordGuildID {
		return
	}
	b.syncGuildRoles(s)
}

func (b *Bot) onGuildRoleDelete(s *discordgo.Session, e *discordgo.GuildRoleDelete) {
	if e == nil || e.GuildID != b.Cfg.DiscordGuildID {
		return
	}
	b.syncGuildRoles(s)
}

func commandDefs(prefix string) []*discordgo.ApplicationCommand {
	return []*discordgo.ApplicationCommand{
		{
			Name:        prefix + "sso",
			Description: "SSO API tokens",
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "get", Description: "Get or create your SSO token and " + web.DesktopAppName + " source JSON"},
				{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "revoke", Description: "Revoke your SSO token",
					Options: []*discordgo.ApplicationCommandOption{
						{Type: discordgo.ApplicationCommandOptionInteger, Name: "id", Description: "Token id (optional; defaults to your active token)", Required: false},
					}},
				{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "list", Description: "Show your active token metadata"},
			},
		},
		{
			Name:        prefix + "whoami",
			Description: "Show your identity status",
		},
	}
}

func (b *Bot) onInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	ctx := context.Background()
	data := i.ApplicationCommandData()
	uid, uname, roles := interactionIdentity(i)
	if uid == "" {
		b.Log.Error("discord interaction missing user")
		b.respondErr(s, i, "Could not resolve your Discord user.")
		return
	}
	sub := ""
	if len(data.Options) > 0 {
		sub = data.Options[0].Name
	}
	b.Log.Info("discord command", "command", data.Name, "subcommand", sub, "discord_id", uid, "user", uname)

	u, err := b.Store.UpsertUser(ctx, uid, uname, roles)
	if err != nil {
		b.Log.Error("upsert user", "err", err, "discord_id", uid)
		b.respondErr(s, i, "Database error while loading your user.")
		return
	}
	if err := b.Store.EnsureUserInDefaultGroupIfNone(ctx, u); err != nil {
		b.Log.Error("default group assign", "err", err, "user_id", u.ID)
		b.respondErr(s, i, "Database error while assigning your group.")
		return
	}

	cmdKey := strings.TrimPrefix(data.Name, b.Cfg.DiscordCommandPrefix)
	if !b.userIsDiscordAdmin(u) {
		allowed, err := b.Store.UserCanUseDiscordCommand(ctx, u, cmdKey)
		if err != nil {
			b.Log.Error("discord command access", "err", err, "command", cmdKey, "user_id", u.ID)
			b.respondErr(s, i, "Could not verify command access.")
			return
		}
		if !allowed {
			b.respondErr(s, i, fmt.Sprintf(
				"You don't have permission to use %s.\n\nAsk a guild admin to add you to a group with Discord slash command access.",
				b.slash(cmdKey),
			))
			return
		}
	}

	switch data.Name {
	case b.cmd("sso"):
		b.handleSSO(ctx, s, i, u, data)
	case b.cmd("whoami"):
		// Prefer freshly cached roles from DB (periodic sync) when interaction roles are empty.
		if len(roles) == 0 {
			if fresh, err := b.Store.UserByID(ctx, u.ID); err == nil {
				u = fresh
			}
		}
		ids, _ := b.Store.AllowedAccountIDs(ctx, u)
		roleLine := "_none_"
		if len(u.RoleIDs) > 0 {
			parts := make([]string, 0, len(u.RoleIDs))
			for _, r := range u.RoleIDs {
				parts = append(parts, "<@&"+r+">")
			}
			roleLine = strings.Join(parts, " ")
		}
		b.respondEmbed(s, i, &discordgo.MessageEmbed{
			Title:       "Your identity",
			Color:       colorInfo,
			Description: "SSO token holders get **base** accounts automatically. **Elevated** accounts also require the Discord role set on that account.\nRoles refresh automatically from Discord.",
			Fields: []*discordgo.MessageEmbedField{
				{Name: "Discord", Value: fmt.Sprintf("<@%s>\n`%s`", u.DiscordID, u.DiscordID), Inline: true},
				{Name: "User ID", Value: fmt.Sprintf("`%d`", u.ID), Inline: true},
				{Name: "SSO accounts you can use", Value: fmt.Sprintf("`%d`", len(ids)), Inline: true},
				{Name: "Cached roles", Value: roleLine, Inline: false},
			},
		})
	}
}

func interactionIdentity(i *discordgo.InteractionCreate) (id, name string, roles []string) {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID, i.Member.User.Username, i.Member.Roles
	}
	if i.User != nil {
		return i.User.ID, i.User.Username, nil
	}
	return "", "", nil
}

func (b *Bot) userIsDiscordAdmin(u store.User) bool {
	for _, id := range b.Cfg.DiscordBootstrapAdmins {
		if u.DiscordID == id {
			return true
		}
	}
	if b.Cfg.DiscordAdminRoleID != "" {
		for _, r := range u.RoleIDs {
			if r == b.Cfg.DiscordAdminRoleID {
				return true
			}
		}
	}
	return false
}

func (b *Bot) handleSSO(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, u store.User, data discordgo.ApplicationCommandInteractionData) {
	sub := data.Options[0]
	switch sub.Name {
	case "revoke":
		id := optInt(sub, "id")
		if err := b.Store.RevokeToken(ctx, u.ID, id); err != nil {
			b.Log.Error("token revoke", "err", err, "token_id", id, "user_id", u.ID)
			b.respondErr(s, i, err.Error())
			return
		}
		b.Log.Info("token revoked", "token_id", id, "user_id", u.ID)
		msg := "Your SSO token has been revoked."
		if id > 0 {
			msg = fmt.Sprintf("Token `#%d` is no longer valid.", id)
		}
		b.respondOK(s, i, "Token revoked", msg)
	case "list":
		t, ok, err := b.Store.ActiveToken(ctx, u.ID)
		if err != nil {
			b.Log.Error("token list", "err", err, "user_id", u.ID)
			b.respondErr(s, i, err.Error())
			return
		}
		b.Log.Info("token list", "user_id", u.ID, "discord_id", u.DiscordID, "has_token", ok)
		if !ok {
			b.respondEmbed(s, i, &discordgo.MessageEmbed{
				Title:       "SSO token",
				Description: fmt.Sprintf("No active token for <@%s>.\n\nRun `%s get` to create one.", u.DiscordID, b.slash("sso")),
				Color:       colorInfo,
			})
			return
		}
		last := "_never_"
		if t.LastUsed != nil {
			last = fmtTime(*t.LastUsed)
		}
		b.respondEmbed(s, i, &discordgo.MessageEmbed{
			Title: "SSO token",
			Color: colorInfo,
			Fields: []*discordgo.MessageEmbedField{
				{Name: "Created", Value: fmtTime(t.CreatedAt), Inline: true},
				{Name: "Last used", Value: last, Inline: true},
			},
			Footer: &discordgo.MessageEmbedFooter{Text: fmt.Sprintf("Use %s get for the secret and GUI JSON · one token per Discord user", b.slash("sso"))},
		})
	case "get":
		secret, created, err := b.resolveSSOTokenSecret(ctx, u.ID)
		if err != nil {
			b.Log.Error("token get", "err", err, "user_id", u.ID)
			b.respondErr(s, i, err.Error())
			return
		}
		if created {
			b.Store.Audit(ctx, u.ID, "token_create", "via sso get")
			b.Log.Info("token created via get", "user_id", u.ID, "discord_id", u.DiscordID)
		} else {
			b.Log.Info("token get", "user_id", u.ID, "discord_id", u.DiscordID)
		}
		name := web.SourceNameFromConfig(b.Cfg)
		host := web.SourceHostFromConfig(b.Cfg)
		jsonSnippet := web.BuildSourceImportJSON(name, host, secret, "")
		desc := "Keep this private. Paste the JSON into " + web.DesktopAppName + " → Connections → Add from JSON."
		if created {
			desc = "New SSO token created (one per Discord user). Paste the JSON into " + web.DesktopAppName + " → Connections → Add from JSON."
		}
		b.respondEmbed(s, i, &discordgo.MessageEmbed{
			Title:       "Your SSO token",
			Description: desc,
			Color:       colorOK,
			Fields: []*discordgo.MessageEmbedField{
				{Name: "Secret", Value: fmt.Sprintf("```\n%s\n```", secret), Inline: false},
				{Name: web.DesktopAppName + " source", Value: fmt.Sprintf("```json\n%s\n```", jsonSnippet), Inline: false},
			},
		})
	}
}

func (b *Bot) resolveSSOTokenSecret(ctx context.Context, userID int64) (raw string, created bool, err error) {
	t, ok, err := b.Store.ActiveToken(ctx, userID)
	if err != nil {
		return "", false, err
	}
	if ok && t.HasSecret {
		return t.Raw, false, nil
	}
	raw, _, err = b.Store.CreateToken(ctx, userID)
	if err != nil {
		return "", false, err
	}
	return raw, true, nil
}

const (
	colorOK   = 0x3BA55D // green
	colorErr  = 0xED4245 // red
	colorInfo = 0x5865F2 // blurple
)

func (b *Bot) respondOK(s *discordgo.Session, i *discordgo.InteractionCreate, title, body string) {
	b.respondEmbed(s, i, &discordgo.MessageEmbed{
		Title:       title,
		Description: body,
		Color:       colorOK,
	})
}

func (b *Bot) respondErr(s *discordgo.Session, i *discordgo.InteractionCreate, msg string) {
	b.respondEmbed(s, i, &discordgo.MessageEmbed{
		Title:       "Error",
		Description: msg,
		Color:       colorErr,
	})
}

func (b *Bot) respondEmbed(s *discordgo.Session, i *discordgo.InteractionCreate, emb *discordgo.MessageEmbed) {
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags:  discordgo.MessageFlagsEphemeral,
			Embeds: []*discordgo.MessageEmbed{emb},
		},
	}); err != nil {
		b.Log.Error("discord respond", "err", err)
	}
}

func fmtTime(v any) string {
	switch t := v.(type) {
	case time.Time:
		return t.UTC().Format("2006-01-02 15:04 UTC")
	default:
		return fmt.Sprint(v)
	}
}

func optInt(sub *discordgo.ApplicationCommandInteractionDataOption, name string) int64 {
	for _, o := range sub.Options {
		if o.Name == name {
			return o.IntValue()
		}
	}
	return 0
}
