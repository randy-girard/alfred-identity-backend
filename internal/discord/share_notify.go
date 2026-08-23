package discord

import (
	"context"
	"fmt"
	"strings"

	"github.com/alfred-identity/web/internal/store"
)

// NotifyAccountShared sends a Discord DM to each newly granted share recipient.
// Failures are logged; the share operation itself is never rolled back.
func (b *Bot) NotifyAccountShared(ctx context.Context, owner store.User, accountUsername string, aliases []string, recipientUserIDs []int64) {
	if b == nil || b.Session == nil || len(recipientUserIDs) == 0 {
		return
	}
	text := formatShareNotifyMessage(owner, accountUsername, aliases)
	for _, uid := range recipientUserIDs {
		recipient, err := b.Store.UserByID(ctx, uid)
		if err != nil {
			b.Log.Warn("share notify: recipient lookup", "user_id", uid, "err", err)
			continue
		}
		discordID := strings.TrimSpace(recipient.DiscordID)
		if discordID == "" {
			b.Log.Warn("share notify: recipient has no discord id", "user_id", uid)
			continue
		}
		if err := b.sendDirectMessage(discordID, text); err != nil {
			b.Log.Warn("share notify: dm failed",
				"recipient_discord_id", discordID,
				"recipient_user_id", uid,
				"account", accountUsername,
				"err", err,
			)
		} else {
			b.Log.Info("share notify: dm sent",
				"recipient_discord_id", discordID,
				"recipient_user_id", uid,
				"account", accountUsername,
				"owner_user_id", owner.ID,
			)
		}
	}
}

func (b *Bot) sendDirectMessage(discordUserID, content string) error {
	ch, err := b.Session.UserChannelCreate(discordUserID)
	if err != nil {
		return fmt.Errorf("open dm channel: %w", err)
	}
	_, err = b.Session.ChannelMessageSend(ch.ID, content)
	if err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	return nil
}

func formatShareNotifyMessage(owner store.User, accountUsername string, aliases []string) string {
	ownerLabel := strings.TrimSpace(owner.DisplayName)
	if ownerLabel == "" {
		ownerLabel = "Someone"
	}
	if id := strings.TrimSpace(owner.DiscordID); id != "" {
		ownerLabel = fmt.Sprintf("<@%s> (%s)", id, ownerLabel)
	}

	var b strings.Builder
	b.WriteString("**Alfred Identity** — account shared with you\n\n")
	fmt.Fprintf(&b, "**Owner:** %s\n", ownerLabel)
	fmt.Fprintf(&b, "**Account:** %s\n", accountUsername)
	if cleaned := cleanShareAliases(accountUsername, aliases); len(cleaned) > 0 {
		fmt.Fprintf(&b, "**Aliases:** %s\n", strings.Join(cleaned, ", "))
	}
	b.WriteString("\nYou can log in through **Alfred Identity** using SSO. The shared account appears when you are connected.\n\n")
	b.WriteString("This is a private share — only users the owner selects can use it.")
	return b.String()
}

func cleanShareAliases(accountUsername string, aliases []string) []string {
	out := make([]string, 0, len(aliases))
	seen := map[string]bool{}
	for _, al := range aliases {
		al = strings.TrimSpace(al)
		if al == "" || strings.EqualFold(al, accountUsername) || seen[strings.ToLower(al)] {
			continue
		}
		seen[strings.ToLower(al)] = true
		out = append(out, al)
	}
	return out
}
