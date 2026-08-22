#!/usr/bin/env python3
"""Build a clean HTML coverage report from a Go coverprofile."""

from __future__ import annotations

import argparse
import html
import re
import subprocess
import sys
import tempfile
from collections import defaultdict
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path


FUNC_LINE = re.compile(
    r"^(?P<file>.+):(?P<line>\d+):\s+(?P<func>\S+)\s+(?P<pct>[\d.]+)%$"
)
TOTAL_LINE = re.compile(r"^total:\s+\(statements\)\s+(?P<pct>[\d.]+)%$")
OPTION_RE = re.compile(
    r'<option value="(?P<id>file\d+)">(?P<label>[^<]*)</option>',
    re.I,
)
PRE_RE = re.compile(
    r'<pre class="file" id="(?P<id>file\d+)"[^>]*>(?P<body>.*?)</pre>',
    re.I | re.S,
)

SHARED_CSS = """
:root {
  --bg: #f6f7f9;
  --panel: #fff;
  --fg: #1a1a1e;
  --muted: #6b6b76;
  --line: #e2e4ea;
  --good: #1a7f37;
  --ok: #9a6700;
  --low: #cf222e;
  --none: #8c959f;
  --track: #eef0f4;
  --accent: #1f6feb;
  --code-bg: #fafbfc;
  --cov0-bg: #ffebe9;
  --cov0-fg: #82071e;
  --cov-mid-bg: #dafbe1;
  --cov-mid-fg: #0a3069;
  --cov-hi-bg: #acf2bd;
  --cov-hi-fg: #0a3069;
  --untracked: #656d76;
  --row-hover: #f0f3f8;
}
* { box-sizing: border-box; }
body {
  margin: 0;
  font: 14px/1.45 -apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif;
  color: var(--fg);
  background: var(--bg);
}
.wrap { max-width: 1100px; margin: 0 auto; padding: 2rem 1.25rem 3rem; }
header.page-header {
  background: var(--panel);
  border: 1px solid var(--line);
  border-radius: 12px;
  padding: 1.25rem 1.5rem;
  display: flex;
  flex-wrap: wrap;
  gap: 1rem;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 1.25rem;
}
h1 { margin: 0; font-size: 1.25rem; letter-spacing: -0.02em; }
.meta { color: var(--muted); font-size: 0.85rem; margin-top: 0.25rem; }
.score { text-align: right; min-width: 7rem; }
.score .value {
  font-size: 2rem; font-weight: 700; letter-spacing: -0.03em; line-height: 1;
}
.score .label { color: var(--muted); font-size: 0.8rem; margin-top: 0.2rem; }
.score.good .value { color: var(--good); }
.score.ok .value { color: var(--ok); }
.score.low .value, .score.none .value { color: var(--low); }
.panel {
  background: var(--panel);
  border: 1px solid var(--line);
  border-radius: 12px;
  overflow: hidden;
  margin-bottom: 1rem;
}
.panel h2 {
  margin: 0;
  padding: 0.85rem 1.1rem;
  font-size: 0.9rem;
  border-bottom: 1px solid var(--line);
  background: #fafbfc;
}
table { width: 100%; border-collapse: collapse; }
th, td { padding: 0.65rem 1.1rem; text-align: left; border-bottom: 1px solid var(--line); }
th {
  color: var(--muted); font-size: 0.75rem; font-weight: 600;
  text-transform: uppercase; letter-spacing: 0.04em;
}
tr:last-child td { border-bottom: 0; }
tr.file-row { cursor: pointer; }
tr.file-row:hover { background: var(--row-hover); }
a.row-link {
  color: inherit;
  text-decoration: none;
  display: block;
}
a.row-link:hover code { color: var(--accent); }
.pkg code, .file code {
  font: 12px/1.4 ui-monospace, SFMono-Regular, Menlo, monospace;
  color: var(--fg);
}
.file .name { font-weight: 600; }
.file .dir { color: var(--muted); display: block; margin-top: 0.1rem; font-size: 0.75rem; }
.num { width: 4rem; color: var(--muted); }
.pct { width: 4.5rem; font-variant-numeric: tabular-nums; font-weight: 600; }
.pct.good { color: var(--good); }
.pct.ok { color: var(--ok); }
.pct.low, .pct.none { color: var(--low); }
.bar { width: 40%; }
.track {
  height: 0.55rem; background: var(--track); border-radius: 999px; overflow: hidden;
}
.fill { height: 100%; border-radius: 999px; }
.fill.good { background: var(--good); }
.fill.ok { background: #d4a72c; }
.fill.low, .fill.none { background: var(--low); }
.actions {
  display: flex; flex-wrap: wrap; gap: 0.65rem; padding: 1rem 1.1rem; align-items: center;
}
a.btn {
  display: inline-block;
  padding: 0.45rem 0.85rem;
  border-radius: 8px;
  border: 1px solid var(--line);
  background: #fff;
  color: var(--fg);
  text-decoration: none;
  font-size: 0.9rem;
}
a.btn.primary { background: var(--accent); border-color: var(--accent); color: #fff; }
a.btn:hover { filter: brightness(0.97); }
footer.page-footer { color: var(--muted); font-size: 0.8rem; margin-top: 1rem; }

.toolbar {
  display: flex; flex-wrap: wrap; gap: 0.75rem; align-items: center;
  padding: 0.85rem 1.1rem; border-bottom: 1px solid var(--line); background: #fafbfc;
}
.toolbar label { color: var(--muted); font-size: 0.8rem; font-weight: 600; }
.toolbar select {
  flex: 1 1 16rem;
  min-width: 12rem;
  max-width: 100%;
  font: 13px/1.3 ui-monospace, SFMono-Regular, Menlo, monospace;
  padding: 0.4rem 0.55rem;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: #fff;
  color: var(--fg);
}
.legend {
  display: flex; flex-wrap: wrap; gap: 0.5rem; align-items: center;
  padding: 0.65rem 1.1rem; border-bottom: 1px solid var(--line);
  color: var(--muted); font-size: 0.8rem;
}
.legend .swatch {
  display: inline-flex; align-items: center; gap: 0.3rem;
  padding: 0.15rem 0.45rem; border-radius: 6px; border: 1px solid var(--line);
  background: #fff;
}
.legend .dot { width: 0.55rem; height: 0.55rem; border-radius: 50%; }
.legend .dot.miss { background: #ff8182; }
.legend .dot.hit { background: #4ac26b; }
.legend .dot.hot { background: #2da44e; }
.legend .dot.skip { background: #d0d7de; }
#content { padding: 0; }
pre.file {
  display: none;
  margin: 0;
  padding: 1rem 1.1rem 1.5rem;
  overflow: auto;
  background: var(--code-bg);
  color: var(--fg);
  font: 12.5px/1.55 ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  white-space: pre;
  tab-size: 4;
}
pre.file.visible, pre.file.single { display: block; }
pre.file { color: var(--untracked); }
.cov0 { color: var(--cov0-fg); background: var(--cov0-bg); }
.cov1, .cov2, .cov3, .cov4, .cov5 {
  color: var(--cov-mid-fg); background: var(--cov-mid-bg);
}
.cov6, .cov7, .cov8, .cov9, .cov10 {
  color: var(--cov-hi-fg); background: var(--cov-hi-bg);
}
"""


@dataclass
class CoverFile:
    id: str
    path: str
    pct: float
    body: str

    @property
    def dirname(self) -> str:
        if "/" not in self.path:
            return ""
        return self.path.rsplit("/", 1)[0]

    @property
    def basename(self) -> str:
        return self.path.rsplit("/", 1)[-1]


def run_cover_func(profile: Path) -> tuple[float, list[dict]]:
    out = subprocess.check_output(
        ["go", "tool", "cover", f"-func={profile}"],
        text=True,
    )
    rows: list[dict] = []
    total = 0.0
    for line in out.splitlines():
        line = line.strip()
        m = TOTAL_LINE.match(line)
        if m:
            total = float(m.group("pct"))
            continue
        m = FUNC_LINE.match(line)
        if not m:
            continue
        file_path = m.group("file")
        pkg = file_path.rsplit("/", 1)[0] if "/" in file_path else file_path
        rows.append(
            {
                "file": file_path,
                "pkg": pkg,
                "func": m.group("func"),
                "pct": float(m.group("pct")),
            }
        )
    return total, rows


def package_stats(rows: list[dict]) -> list[tuple[str, float, int]]:
    buckets: dict[str, list[float]] = defaultdict(list)
    for r in rows:
        buckets[r["pkg"]].append(r["pct"])
    stats = []
    for pkg, pcts in buckets.items():
        avg = sum(pcts) / len(pcts) if pcts else 0.0
        stats.append((pkg, avg, len(pcts)))
    stats.sort(key=lambda x: (-x[1], x[0]))
    return stats


def pct_class(pct: float) -> str:
    if pct >= 80:
        return "good"
    if pct >= 50:
        return "ok"
    if pct > 0:
        return "low"
    return "none"


def short_path(path: str, module_hint: str) -> str:
    for prefix in (module_hint + "/", "github.com/alfred-identity/"):
        if path.startswith(prefix):
            return path[len(prefix) :]
    return path


def parse_option_label(label: str, module_hint: str) -> tuple[str, float]:
    label = label.strip()
    m = re.match(r"^(.*)\s+\(([\d.]+)%\)$", label)
    if m:
        return short_path(m.group(1).strip(), module_hint), float(m.group(2))
    return short_path(label, module_hint), 0.0


def parse_cover_files(raw: str, module_hint: str) -> list[CoverFile]:
    options = list(OPTION_RE.finditer(raw))
    pres = {m.group("id"): m.group("body") for m in PRE_RE.finditer(raw)}
    if not options or not pres:
        raise ValueError("could not parse go tool cover HTML")
    files: list[CoverFile] = []
    for m in options:
        fid = m.group("id")
        body = pres.get(fid)
        if body is None:
            continue
        path, pct = parse_option_label(m.group("label"), module_hint)
        files.append(CoverFile(id=fid, path=path, pct=pct, body=body))
    return files


def legend_html() -> str:
    return """
      <div class="legend">
        <span class="swatch"><span class="dot skip"></span> not tracked</span>
        <span class="swatch"><span class="dot miss"></span> no coverage</span>
        <span class="swatch"><span class="dot hit"></span> covered</span>
        <span class="swatch"><span class="dot hot"></span> high coverage</span>
      </div>
"""


def write_index(
    out_dir: Path,
    title: str,
    total: float,
    pkg_stats: list[tuple[str, float, int]],
    files: list[CoverFile],
    module_hint: str,
) -> None:
    generated = datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M UTC")
    pkg_rows = []
    for pkg, avg, nfn in pkg_stats:
        label = html.escape(short_path(pkg, module_hint))
        cls = pct_class(avg)
        pkg_rows.append(
            f"""<tr>
  <td class="pkg"><code>{label}</code></td>
  <td class="num">{nfn}</td>
  <td class="pct {cls}">{avg:.1f}%</td>
  <td class="bar"><div class="track"><div class="fill {cls}" style="width:{min(avg, 100):.1f}%"></div></div></td>
</tr>"""
        )

    file_rows = []
    for f in sorted(files, key=lambda x: (-x.pct, x.path.lower())):
        cls = pct_class(f.pct)
        href = html.escape(f"files/{f.id}.html")
        name = html.escape(f.basename)
        dirn = html.escape(f.dirname) if f.dirname else ""
        dir_html = f'<span class="dir">{dirn}</span>' if dirn else ""
        file_rows.append(
            f"""<tr class="file-row" onclick="location.href='{href}'">
  <td class="file">
    <a class="row-link" href="{href}">
      <code class="name">{name}</code>{dir_html}
    </a>
  </td>
  <td class="pct {cls}">{f.pct:.1f}%</td>
  <td class="bar"><div class="track"><div class="fill {cls}" style="width:{min(f.pct, 100):.1f}%"></div></div></td>
</tr>"""
        )

    total_cls = pct_class(total)
    page = f"""<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8"/>
  <meta name="viewport" content="width=device-width, initial-scale=1"/>
  <title>{html.escape(title)} — coverage</title>
  <style>{SHARED_CSS}</style>
</head>
<body>
  <div class="wrap">
    <header class="page-header">
      <div>
        <h1>{html.escape(title)}</h1>
        <div class="meta">Statement coverage · generated {html.escape(generated)}</div>
      </div>
      <div class="score {total_cls}">
        <div class="value">{total:.1f}%</div>
        <div class="label">overall</div>
      </div>
    </header>

    <section class="panel">
      <h2>Files</h2>
      <table>
        <thead>
          <tr>
            <th>File</th>
            <th>Coverage</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          {"".join(file_rows) if file_rows else '<tr><td colspan="3">No files.</td></tr>'}
        </tbody>
      </table>
    </section>

    <section class="panel">
      <h2>Packages</h2>
      <table>
        <thead>
          <tr>
            <th>Package</th>
            <th>Funcs</th>
            <th>Avg</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          {"".join(pkg_rows) if pkg_rows else '<tr><td colspan="4">No packages.</td></tr>'}
        </tbody>
      </table>
    </section>

    <section class="panel">
      <h2>Browse</h2>
      <div class="actions">
        <a class="btn primary" href="source.html">All files viewer</a>
        <a class="btn" href="coverage.out">Raw profile</a>
        <a class="btn" href="func.txt">Function report</a>
      </div>
    </section>

    <footer class="page-footer">
      Click a file to open its annotated coverage report. Package averages are per-function means;
      overall % is Go’s statement coverage total.
    </footer>
  </div>
</body>
</html>
"""
    (out_dir / "index.html").write_text(page, encoding="utf-8")


def write_file_pages(
    out_dir: Path,
    title: str,
    files: list[CoverFile],
) -> None:
    files_dir = out_dir / "files"
    files_dir.mkdir(parents=True, exist_ok=True)
    by_id = {f.id: i for i, f in enumerate(files)}

    for i, f in enumerate(files):
        cls = pct_class(f.pct)
        prev_html = ""
        next_html = ""
        if i > 0:
            p = files[i - 1]
            prev_html = f'<a class="btn" href="{html.escape(p.id)}.html">← {html.escape(p.basename)}</a>'
        if i + 1 < len(files):
            n = files[i + 1]
            next_html = f'<a class="btn" href="{html.escape(n.id)}.html">{html.escape(n.basename)} →</a>'

        page = f"""<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8"/>
  <meta name="viewport" content="width=device-width, initial-scale=1"/>
  <title>{html.escape(f.basename)} — {html.escape(title)}</title>
  <style>{SHARED_CSS}</style>
</head>
<body>
  <div class="wrap">
    <header class="page-header">
      <div>
        <h1>{html.escape(f.basename)}</h1>
        <div class="meta">{html.escape(f.path)} · file coverage</div>
      </div>
      <div class="score {cls}">
        <div class="value">{f.pct:.1f}%</div>
        <div class="label">this file</div>
      </div>
    </header>

    <div class="actions" style="padding: 0 0 1rem;">
      <a class="btn" href="../index.html">← All files</a>
      {prev_html}
      {next_html}
    </div>

    <section class="panel">
      {legend_html()}
      <div id="content">
        <pre class="file single">{f.body}</pre>
      </div>
    </section>

    <footer class="page-footer">
      {html.escape(title)} · {i + 1} of {len(files)}
    </footer>
  </div>
</body>
</html>
"""
        (files_dir / f"{f.id}.html").write_text(page, encoding="utf-8")
        _ = by_id  # silence unused if empty


def write_source_viewer(
    out_dir: Path,
    title: str,
    total: float,
    files: list[CoverFile],
) -> None:
    opt_html = []
    files_html = []
    for f in files:
        label = f"{f.path} ({f.pct:.1f}%)"
        opt_html.append(
            f'<option value="{html.escape(f.id)}">{html.escape(label)}</option>'
        )
        files_html.append(
            f'<pre class="file" id="{html.escape(f.id)}">{f.body}</pre>'
        )

    generated = datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M UTC")
    total_cls = pct_class(total)
    page = f"""<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8"/>
  <meta name="viewport" content="width=device-width, initial-scale=1"/>
  <title>{html.escape(title)} — annotated source</title>
  <style>{SHARED_CSS}</style>
</head>
<body>
  <div class="wrap">
    <header class="page-header">
      <div>
        <h1>{html.escape(title)}</h1>
        <div class="meta">All files viewer · generated {html.escape(generated)}</div>
      </div>
      <div class="score {total_cls}">
        <div class="value">{total:.1f}%</div>
        <div class="label">overall</div>
      </div>
    </header>

    <div class="actions" style="padding: 0 0 1rem;">
      <a class="btn" href="index.html">← All files</a>
    </div>

    <section class="panel">
      <div class="toolbar">
        <label for="files">File</label>
        <select id="files">
          {"".join(opt_html)}
        </select>
      </div>
      {legend_html()}
      <div id="content">
        {"".join(files_html)}
      </div>
    </section>

    <footer class="page-footer">
      Prefer the file list on the index for one-page reports. This viewer keeps every file in one place.
    </footer>
  </div>
  <script>
  (function () {{
    var files = document.getElementById('files');
    var visible = null;
    function select(part) {{
      if (visible) visible.classList.remove('visible');
      visible = document.getElementById(part);
      if (!visible) return;
      files.value = part;
      visible.classList.add('visible');
      location.hash = part;
    }}
    files.addEventListener('change', function () {{
      select(files.value);
      window.scrollTo(0, 0);
    }});
    if (location.hash) select(location.hash.slice(1));
    if (!visible) select('file0');
  }})();
  </script>
</body>
</html>
"""
    (out_dir / "source.html").write_text(page, encoding="utf-8")


def generate_raw_cover_html(profile: Path) -> str:
    with tempfile.NamedTemporaryFile(suffix=".html", delete=False) as tmp:
        tmp_path = Path(tmp.name)
    try:
        subprocess.check_call(
            ["go", "tool", "cover", f"-html={profile}", "-o", str(tmp_path)]
        )
        return tmp_path.read_text(encoding="utf-8", errors="replace")
    finally:
        tmp_path.unlink(missing_ok=True)


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--profile", required=True, type=Path)
    ap.add_argument("--out", required=True, type=Path)
    ap.add_argument("--title", required=True)
    ap.add_argument("--module", default="", help="Module path prefix to strip in labels")
    args = ap.parse_args()

    profile: Path = args.profile
    out_dir: Path = args.out
    if not profile.is_file():
        print(f"missing coverprofile: {profile}", file=sys.stderr)
        return 1

    out_dir.mkdir(parents=True, exist_ok=True)

    dest_profile = out_dir / "coverage.out"
    if profile.resolve() != dest_profile.resolve():
        dest_profile.write_bytes(profile.read_bytes())

    total, rows = run_cover_func(dest_profile)
    func_txt = subprocess.check_output(
        ["go", "tool", "cover", f"-func={dest_profile}"],
        text=True,
    )
    (out_dir / "func.txt").write_text(func_txt, encoding="utf-8")

    module_hint = args.module or ""
    raw = generate_raw_cover_html(dest_profile)
    files = parse_cover_files(raw, module_hint)

    write_index(out_dir, args.title, total, package_stats(rows), files, module_hint)
    write_file_pages(out_dir, args.title, files)
    write_source_viewer(out_dir, args.title, total, files)

    print(f"total: (statements) {total:.1f}%")
    print(f"Wrote {out_dir / 'index.html'} ({len(files)} files)")
    print(f"Wrote {out_dir / 'files'}/*.html")
    print(f"Wrote {out_dir / 'source.html'}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
