package web

import (
	"encoding/json"
	"html/template"
	"net/http"
	"strings"
)

const openAlfredPath = "/open-alfred"

var openAlfredTmpl = template.Must(template.New("open-alfred").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Open {{.AppName}}</title>
<style>
  body { font-family: system-ui, sans-serif; max-width: 36rem; margin: 4rem auto; padding: 0 1.5rem; line-height: 1.45; color: #1a1a1a; }
  a.btn { display: inline-block; background: #3BA55D; color: #fff; text-decoration: none; padding: 0.7rem 1.1rem; border-radius: 8px; font-weight: 600; }
  .hint { color: #555; margin-top: 1.5rem; }
  code { font-size: 0.95em; }
</style>
</head>
<body>
{{if .Error}}
<h1>This link is invalid</h1>
<p>{{.Error}}</p>
<p class="hint">Run <code>/alfred-identity-sso get</code> in Discord and use <strong>Open in {{.AppName}}</strong> again.</p>
{{else if .AppURL}}
<h1>Opening {{.AppName}}…</h1>
<p>Your browser should ask to open the app. Confirm, then add the source when {{.AppName}} prompts.</p>
<p><a class="btn" id="open" href="{{.AppURL}}">Open {{.AppName}}</a></p>
<p class="hint">If nothing happens, install {{.AppName}} and click the button again. You can also paste the JSON from Discord into Connections → Add from JSON.</p>
<script>location.replace({{.AppURLJS}});</script>
{{else}}
<h1>Open {{.AppName}}</h1>
<p>This page opens {{.AppName}} with your SSO source from Discord.</p>
<p><a class="btn" id="open" href="#">Open {{.AppName}}</a></p>
<p class="hint">If the button does nothing, paste the JSON from <code>/alfred-identity-sso get</code> into Connections → Add from JSON.</p>
<script>
(function () {
  const params = new URLSearchParams(location.search);
  let d = params.get("d") || "";
  if (!d && location.hash) {
    const h = location.hash.replace(/^#/, "");
    d = h.replace(/^d=/, "");
  }
  if (!d) return;
  const app = {{.SchemeJS}} + "://import?d=" + encodeURIComponent(d);
  const a = document.getElementById("open");
  if (a) a.href = app;
  location.replace(app);
})();
</script>
{{end}}
</body>
</html>`))

type openAlfredPage struct {
	AppName  string
	AppURL   string
	AppURLJS template.JS
	SchemeJS template.JS
	Error    string
}

// HandleOpenAlfred is a public landing page Discord can link to; it opens the desktop app.
func HandleOpenAlfred(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	page := openAlfredPage{
		AppName:  DesktopAppName,
		SchemeJS: jsonJS(AppURLScheme),
	}
	d := strings.TrimSpace(r.URL.Query().Get("d"))
	if d != "" {
		src, err := ParseSourceDeepLinkPayload(d)
		if err != nil {
			page.Error = "The source data in this link could not be read."
			writeOpenAlfred(w, http.StatusBadRequest, page)
			return
		}
		appURL, err := EncodeAppSchemeURL(src.Name, src.Host, src.Token)
		if err != nil {
			page.Error = "The source data in this link could not be read."
			writeOpenAlfred(w, http.StatusBadRequest, page)
			return
		}
		page.AppURL = appURL
		page.AppURLJS = jsonJS(appURL)
	}
	writeOpenAlfred(w, http.StatusOK, page)
}

func writeOpenAlfred(w http.ResponseWriter, status int, page openAlfredPage) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	_ = openAlfredTmpl.Execute(w, page)
}

func jsonJS(s string) template.JS {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return template.JS(b)
}
