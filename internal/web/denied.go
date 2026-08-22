package web

import (
	"fmt"
	"net/http"
)

func (s *Server) handleDenied(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	reason := r.URL.Query().Get("reason")
	title := "Access denied"
	detail := "Your Discord account is not allowed to use the " + AppName + " web admin."
	switch reason {
	case "revoked":
		title = "SSO access revoked"
		detail = "Your SSO access has been revoked. Contact a guild admin if you think this is a mistake."
	case "not_authorized":
		title = "Not authorized"
		detail = "You must be a member of an access group that grants web UI access (admin or read-only), or hold the Discord admin role. Ask a guild admin to add you to a group with web access enabled."
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_, _ = fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8"/>
  <meta name="viewport" content="width=device-width, initial-scale=1"/>
  <title>%s · %s</title>
  <style>
    :root { color-scheme: light dark; --bg:#0f1419; --card:#1a222c; --text:#e8eef4; --muted:#9aa7b5; --accent:#6cb6ff; --border:#2a3542; }
    @media (prefers-color-scheme: light) {
      :root { --bg:#f4f6f8; --card:#fff; --text:#1a222c; --muted:#5a6a7a; --accent:#0b6bcb; --border:#d5dde5; }
    }
    body { margin:0; min-height:100vh; display:grid; place-items:center; font-family: ui-sans-serif, system-ui, -apple-system, Segoe UI, sans-serif;
      background: var(--bg); color: var(--text); padding: 1.5rem; }
    .card { max-width: 32rem; width: 100%%; background: var(--card); border: 1px solid var(--border); border-radius: 12px; padding: 1.75rem 1.5rem; }
    h1 { margin: 0 0 0.5rem; font-size: 1.35rem; }
    p { margin: 0 0 1.25rem; color: var(--muted); line-height: 1.5; }
    a { color: var(--accent); }
    .actions { display: flex; gap: 0.75rem; flex-wrap: wrap; }
    .btn { display: inline-block; padding: 0.55rem 0.9rem; border-radius: 8px; text-decoration: none;
      background: var(--accent); color: #fff; font-weight: 600; }
    .btn.secondary { background: transparent; color: var(--text); border: 1px solid var(--border); font-weight: 500; }
  </style>
</head>
<body>
  <div class="card">
    <h1>%s</h1>
    <p>%s</p>
    <div class="actions">
      <a class="btn" href="%s/login">Try another Discord account</a>
      <a class="btn secondary" href="%s/logout">Sign out</a>
    </div>
  </div>
</body>
</html>`, title, AppName, title, detail, BasePath, BasePath)
}

func (s *Server) rejectIfReadonly(w http.ResponseWriter, r *http.Request) bool {
	if currentWebRole(r) == webRoleAdmin {
		return false
	}
	// Fallback if role missing from context.
	u := currentUser(r)
	ctx := r.Context()
	if s.isWebAdmin(ctx, u) {
		return false
	}
	writeErr(w, http.StatusForbidden, "readonly")
	return true
}
