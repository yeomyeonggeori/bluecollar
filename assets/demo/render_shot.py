"""Render a saved bluecollar ANSI capture to a styled HTML page.

Usage: python3 render_shot.py <capture.ansi> <output.html> [header-command-line]
Re-rendering never requires re-running the model: the .ansi captures in this
directory are the recorded sessions. Screenshot the HTML in a browser to get
the PNG. The realign pass reproduces the fixed ledger columns for captures
recorded before the CLI aligned them itself.
"""

import html
import re
import sys

PALETTE_256 = {
    117: "#79c0ff",
    141: "#d2a8ff",
    84: "#7ee787",
    222: "#e3b341",
    110: "#a5d6ff",
    210: "#ff7b72",
    243: "#8b949e",
    68: "#3a74d4",
    255: "#f5f7fa",
}
PALETTE_BASIC = {
    31: "#ff7b72",
    32: "#7ee787",
    33: "#e3b341",
    35: "#d2a8ff",
    36: "#79c0ff",
}

PAGE = """<!DOCTYPE html><html><head><meta charset="utf-8"><style>
body {{ background:#010409; margin:0; padding:56px; display:inline-block; }}
.window {{ background:#0d1117; border:1px solid rgba(240,246,252,.12); border-radius:12px;
           padding:22px 28px 26px; display:inline-block;
           box-shadow:0 0 0 1px rgba(1,4,9,.8), 0 16px 48px rgba(1,4,9,.85); }}
.dots {{ margin-bottom:16px; }}
.dots span {{ display:inline-block; width:12px; height:12px; border-radius:50%; margin-right:8px; opacity:.9; }}
pre {{ margin:0; font-family:"Hesalche Mono","HesalcheMono","Hesalche",monospace;
       font-size:15px; line-height:1.62; color:#e6edf3; }}
</style></head><body><div class="window">
<div class="dots"><span style="background:#ff5f57"></span><span style="background:#febc2e"></span><span style="background:#28c840"></span></div>
<pre>{body}</pre></div></body></html>"""



LABEL_WIDTH = 12
LINE_SHAPE = re.compile(
    r"^(?P<pre>(?:\x1b\[[0-9;]*m)*)(?P<glyph>[\u25cf\u25c6\u25c7\u25b8\u2713\u2717] )?(?P<label>[a-z_][a-z_.]*)?(?P<post>(?:\x1b\[[0-9;]*m)*)(?P<gap>  +)(?P<value>.*)$"
)
RESULT_SHAPE = re.compile(r"^(?P<indent>\s+)(?P<codes>(?:\x1b\[[0-9;]*m)*)(?P<arrow>\u2192 .*)$")


def realign(text):
    lines = []
    for line in text.split("\n"):
        match = LINE_SHAPE.match(line)
        if match and match.group("label") and not match.group("label").startswith("status"):
            glyph = match.group("glyph") or "  "
            label = match.group("label").ljust(LABEL_WIDTH)
            lines.append(match.group("pre") + glyph + label + match.group("post") + " " + match.group("value"))
            continue
        result = RESULT_SHAPE.match(line)
        if result:
            lines.append(" " * (LABEL_WIDTH + 3) + result.group("codes") + result.group("arrow"))
            continue
        lines.append(line)
    return "\n".join(lines)


def convert(text):
    pattern = re.compile(r"\x1b\[([0-9;]*)m")
    state = {"bold": False, "dim": False, "color": None}
    pieces = []

    def emit(chunk):
        if not chunk:
            return
        css = []
        if state["bold"]:
            css.append("font-weight:700")
        if state["dim"]:
            css.append("color:#8b949e")
        if state["color"]:
            css.append(f"color:{state['color']}")
        escaped = html.escape(chunk)
        pieces.append(f'<span style="{";".join(css)}">{escaped}</span>' if css else escaped)

    position = 0
    for match in pattern.finditer(text):
        emit(text[position:match.start()])
        position = match.end()
        codes = [int(code) for code in match.group(1).split(";") if code != ""] or [0]
        index = 0
        while index < len(codes):
            code = codes[index]
            if code == 0:
                state.update(bold=False, dim=False, color=None)
            elif code == 1:
                state["bold"] = True
            elif code == 2:
                state["dim"] = True
            elif code == 38 and index + 2 < len(codes) and codes[index + 1] == 5:
                state["color"] = PALETTE_256.get(codes[index + 2], "#e6edf3")
                index += 2
            elif code in PALETTE_BASIC:
                state["color"] = PALETTE_BASIC[code]
            index += 1
    emit(text[position:])
    return "".join(pieces)


def main():
    capture_path, output_path = sys.argv[1], sys.argv[2]
    header = sys.argv[3] if len(sys.argv) > 3 else ""
    text = open(capture_path).read()
    text = re.sub(r"(bluecollar\x1b\[0m  [^\n]*?)  ·  /private[^\n]*?  ·  ", r"\1  ·  ", text, count=1)
    text = realign(text)
    if header:
        text = f"\x1b[1m{header}\x1b[0m\n" + text
    open(output_path, "w").write(PAGE.format(body=convert(text)))
    print("wrote", output_path)


main()
