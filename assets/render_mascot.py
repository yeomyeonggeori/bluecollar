"""Render the bluecollar mascot from its cell grid.

Usage: python3 render_mascot.py
Writes mascot.txt (plain blocks), mascot.ansi (terminal colors) and
mascot.png next to this file. Each grid cell prints as two full-block
characters so a cell is square in a terminal; the PNG uses the same
2:1 cell metric. The welcome screen in cmd/bluecollar draws this same
grid at half height with half-block characters.
"""

import pathlib

from PIL import Image

GRID_COLUMNS = 16


def spans(*filled):
    cells = [" "] * GRID_COLUMNS
    for start, end, color in filled:
        for column in range(start, end + 1):
            cells[column] = color
    return cells


ROWS = [
    spans((4, 11, "B")),
    spans((4, 14, "B")),
    spans((4, 11, "S")),
    spans((4, 5, "S"), (7, 8, "S"), (10, 11, "S")),
    spans((4, 11, "S")),
    spans((4, 11, "S")),
    spans((1, 2, "S"), (3, 4, "W"), (5, 5, "B"), (6, 9, "W"), (10, 10, "B"), (11, 12, "W"), (13, 14, "S")),
    spans((4, 11, "B")),
    spans((4, 11, "B")),
    spans((5, 6, "B"), (9, 10, "B")),
    spans((4, 6, "K"), (9, 11, "K")),
]

TERMINAL_COLOR_CODES = {"B": 68, "S": 173, "W": 255, "K": 236}
IMAGE_COLORS = {
    "B": (58, 116, 212, 255),
    "S": (215, 135, 95, 255),
    "W": (245, 247, 250, 255),
    "K": (48, 52, 60, 255),
}
RESET = "\x1b[0m"
BACKDROP = (13, 17, 23, 255)
CELL_WIDTH, CELL_HEIGHT = 16, 32
MARGIN = 32


def write_text(directory):
    plain, ansi = [], []
    for cells in ROWS:
        plain_line, ansi_line = "", ""
        for cell in cells:
            if cell == " ":
                plain_line += "  "
                ansi_line += "  "
            else:
                plain_line += "██"
                ansi_line += f"\x1b[38;5;{TERMINAL_COLOR_CODES[cell]}m██{RESET}"
        plain.append(plain_line.rstrip())
        ansi.append(ansi_line)
    (directory / "mascot.txt").write_text("\n".join(plain) + "\n")
    (directory / "mascot.ansi").write_text("\n".join(ansi) + "\n")


def write_image(directory):
    width = GRID_COLUMNS * CELL_WIDTH + 2 * MARGIN
    height = len(ROWS) * CELL_HEIGHT + 2 * MARGIN
    image = Image.new("RGBA", (width, height), BACKDROP)
    pixels = image.load()
    for row_index, cells in enumerate(ROWS):
        for column_index, cell in enumerate(cells):
            if cell == " ":
                continue
            for offset_y in range(CELL_HEIGHT):
                for offset_x in range(CELL_WIDTH):
                    x = MARGIN + column_index * CELL_WIDTH + offset_x
                    y = MARGIN + row_index * CELL_HEIGHT + offset_y
                    pixels[x, y] = IMAGE_COLORS[cell]
    image.save(directory / "mascot.png")


def main():
    directory = pathlib.Path(__file__).parent
    write_text(directory)
    write_image(directory)
    print("wrote mascot.txt, mascot.ansi, mascot.png")


main()
