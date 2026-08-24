package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	discordAuthURL  = "https://discord.com/api/oauth2/authorize"
	discordTokenURL = "https://discord.com/api/oauth2/token"
	discordAPIBase  = "https://discord.com/api/v10"
)

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	state, err := s.makeOAuthState()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	q := url.Values{}
	q.Set("client_id", s.clientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", s.redirectURI())
	q.Set("scope", "identify guilds.members.read")
	q.Set("state", state)
	http.Redirect(w, r, discordAuthURL+"?"+q.Encode(), http.StatusFound)
}

func (s *Server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if errMsg := r.URL.Query().Get("error"); errMsg != "" {
		http.Error(w, "Discord login denied: "+errMsg, http.StatusForbidden)
		return
	}
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing OAuth code", http.StatusBadRequest)
		return
	}
	if err := s.verifyOAuthState(state); err != nil {
		s.log.Warn("oauth state", "err", err)
		http.Error(w, "invalid OAuth state", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	tok, err := s.exchangeCode(ctx, code)
	if err != nil {
		s.log.Error("oauth token", "err", err)
		http.Error(w, "oauth token exchange failed", http.StatusBadGateway)
		return
	}
	identity, err := s.fetchIdentity(ctx, tok)
	if err != nil {
		s.log.Error("oauth identity", "err", err)
		http.Error(w, "failed to load Discord profile", http.StatusBadGateway)
		return
	}
	roles, err := s.fetchGuildMemberRoles(ctx, tok, identity.ID)
	if err != nil {
		s.log.Error("oauth guild member", "err", err)
		http.Error(w, "you must be a member of the configured Discord guild", http.StatusForbidden)
		return
	}
	display := identity.GlobalName
	if display == "" {
		display = identity.Username
	}
	u, err := s.store.UpsertUser(ctx, identity.ID, display, roles)
	if err != nil {
		s.log.Error("oauth upsert user", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := s.store.EnsureUserInDefaultGroupIfNone(ctx, u); err != nil {
		s.log.Error("oauth default group", "err", err, "user_id", u.ID)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if u.AccessRevoked {
		s.clearSessionCookie(w)
		http.Redirect(w, r, BasePath+"/denied?reason=revoked", http.StatusFound)
		return
	}
	if !s.canAccessWeb(ctx, u) {
		s.clearSessionCookie(w)
		http.Redirect(w, r, BasePath+"/denied?reason=not_authorized", http.StatusFound)
		return
	}
	sess := Session{
		UserID:      u.ID,
		DiscordID:   u.DiscordID,
		DisplayName: u.DisplayName,
		RoleIDs:     u.RoleIDs,
	}
	if err := s.setSessionCookie(w, sess); err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}
	s.log.Info("web login", "user_id", u.ID, "discord_id", u.DiscordID)
	http.Redirect(w, r, BasePath+"/", http.StatusFound)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.clearSessionCookie(w)
	http.Redirect(w, r, BasePath+"/login", http.StatusFound)
}

func (s *Server) redirectURI() string {
	return s.publicURL + BasePath + "/oauth/callback"
}

type discordTokenResp struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
}

type discordUser struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	GlobalName string `json:"global_name"`
}

type discordMember struct {
	Roles []string `json:"roles"`
}

func (s *Server) exchangeCode(ctx context.Context, code string) (string, error) {
	form := url.Values{}
	form.Set("client_id", s.clientID)
	form.Set("client_secret", s.clientSecret)
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", s.redirectURI())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, discordTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return "", fmt.Errorf("token status %d: %s", res.StatusCode, string(body))
	}
	var tok discordTokenResp
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", err
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("empty access token")
	}
	return tok.AccessToken, nil
}

func (s *Server) fetchIdentity(ctx context.Context, accessToken string) (discordUser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discordAPIBase+"/users/@me", nil)
	if err != nil {
		return discordUser{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return discordUser{}, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return discordUser{}, fmt.Errorf("identity status %d: %s", res.StatusCode, string(body))
	}
	var u discordUser
	if err := json.Unmarshal(body, &u); err != nil {
		return discordUser{}, err
	}
	if u.ID == "" {
		return discordUser{}, fmt.Errorf("empty user id")
	}
	return u, nil
}

func (s *Server) fetchGuildMemberRoles(ctx context.Context, accessToken, userID string) ([]string, error) {
	url := fmt.Sprintf("%s/users/@me/guilds/%s/member", discordAPIBase, s.guildID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("member status %d: %s", res.StatusCode, string(body))
	}
	var m discordMember
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	if m.Roles == nil {
		m.Roles = []string{}
	}
	_ = userID
	return m.Roles, nil
}
