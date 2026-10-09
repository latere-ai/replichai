# SPDX-FileCopyrightText: 2026 Latere AI
# SPDX-License-Identifier: AGPL-3.0-or-later

"""Build docs/dispersion-model.tex and docs/dispersion-model.pdf.

The manuscript is the model page with the pilot page as its appendix, so a
reader who cannot open the repository still has the evidence the model answers.
Both pages are written in docs/ as Markdown and stay the source. This script is
the only place that knows how they become one LaTeX document:

    python3 -I tools/manuscript/build.py            # with the DRAFT mark
    python3 -I tools/manuscript/build.py --final    # without it

Run it after editing either page, and commit the .tex and .pdf it writes. It
needs pandoc and tectonic on PATH; tectonic downloads the LaTeX packages it
uses the first time.

A reference to another file in the repository means nothing on paper, so each
one is rewritten below by an exact match, and the build stops when a page gains
one this script does not know.
"""

import os
import re
import shutil
import subprocess
import sys
import tempfile

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
MODEL = os.path.join(ROOT, "docs", "dispersion-model.md")
PILOT = os.path.join(ROOT, "docs", "repeat-runs-pilot.md")
NAME = "dispersion-model"

AUTHOR = "ReplicHAI Workgroup"
INSTITUTE = "Latere AI"
WEBSITE = "replichai.latere.ai"
# The date of the content. Change it when the content changes, not on a rebuild.
DATE = "9 October 2026"

# What the model page says that only a reader of the repository can follow.
MODEL_REWRITES = [
    ("[`repeat-runs-pilot.md`](repeat-runs-pilot.md)", "Appendix A"),
    # A long inline formula would run past the margin, so it is set as a block.
    ("That version fits in brms as `bf(S ~ env + (1|i|paper) + (1|paper:env), sigma ~ env + (1|i|paper))`.",
     "That version fits in brms as\n\n```\nbf(S ~ env + (1|i|paper) + (1|paper:env),\n   sigma ~ env + (1|i|paper))\n```\n"),
    ("## References\n", "## References {.unnumbered}\n"),
]

# The same for the pilot page, which becomes the appendix.
PILOT_REWRITES = [
    ("This page records what", "This appendix records what"),
    ("[`dispersion-model.md`](dispersion-model.md) proposes the measurement, and\n"
     "[spec 046](../specs/046-one-run-is-one-draw.md) the platform changes.",
     "The main text proposes the measurement."),
    ("file kept. The schema is described in\n[`../method/instructions.md`](../method/instructions.md).",
     "file kept."),
    ("[`consultations.md`](consultations.md) §8 proposed fixing the denominator of",
     "An earlier plan proposed fixing the denominator of"),
]

# The pilot page ends by naming the script that prints its tables and how to
# run it. On paper that becomes one sentence.
CHECKED_FROM = "The tables on this page come from"
CHECKED_TO = "The fetch-refusal quotes,"
CHECKED_TEXT = ("The tables in this appendix were computed from the records each run published, "
                "by a script kept with the ReplicHAI source.\n\n")

# The longest table the manuscript sets as one unbreakable block.
MAX_TABLE_ROWS = 12

# pandoc takes a wrapped table's relative column widths from the dashes of its
# rule. The pages write every rule as three dashes, which would give equal
# columns, so each table's rule is given here, keyed by its header row.
TABLE_RULES = {
    "| Symbol | Meaning |":
        "|------|------------------------------|",
    "| Runs per paper $k$ | 95% range of $\\hat H / H$ | Spread | Run-level SD, same data |":
        "|------------------|----------------------|----------|------------------------|",
    "| Stage | Papers × runs | Purpose |":
        "|-----------|-------------------|--------------------|",
    "| Paper | Called below | Runs started | With a verdict |":
        "|--------------------------------------------------|----------------|----------|----------|",
    "| Paper | System | Runs | Inner components | Verified rate, mean and SD | Hidden decisions | Outer-loop rows |":
        "|----------------|-----------|------|--------------|----------------|-----------|----------|",
    "| Paper | Runs with a list | Entries per run | Distinct numbers | In every run | In at least half | In one run only |":
        "|------------------|-----------|-----------|-----------|---------|-----------|-----------|",
}


def rewrite(text, pairs, page):
    for old, new in pairs:
        if text.count(old) != 1:
            sys.exit(f"{page}: expected exactly one {old[:60]!r}, found {text.count(old)}")
        text = text.replace(old, new)
    return text


def set_table_rules(text):
    lines = text.split("\n")
    for index, line in enumerate(lines):
        if line in TABLE_RULES:
            lines[index + 1] = TABLE_RULES[line]
        elif line.startswith("|") and index + 1 < len(lines) and re.fullmatch(r"[|\- ]+", lines[index + 1]) \
                and not re.fullmatch(r"[|\- ]+", line) and (index == 0 or not lines[index - 1].startswith("|")):
            sys.exit(f"a table has no rule in TABLE_RULES: {line}")
    return "\n".join(lines)


def no_repository_links(text, page):
    for label, target in re.findall(r"\[([^\]]*)\]\(([^)\s]+)\)", text):
        if not target.startswith(("https://", "http://")):
            sys.exit(f"{page}: the link [{label}]({target}) points into the repository; add a rewrite for it")


def prepare():
    model = rewrite(open(MODEL, encoding="utf-8").read(), MODEL_REWRITES, "the model page")

    pilot = rewrite(open(PILOT, encoding="utf-8").read(), PILOT_REWRITES, "the pilot page")
    start, end = pilot.index(CHECKED_FROM), pilot.index(CHECKED_TO)
    pilot = pilot[:start] + CHECKED_TEXT + pilot[end:]
    # One level down: the page's title becomes the appendix, its sections the
    # appendix's subsections.
    pilot = re.sub(r"^(#+ )", r"#\1", pilot, flags=re.M)

    no_repository_links(model, "the model page")
    no_repository_links(pilot, "the pilot page")
    return set_table_rules(model.rstrip() + "\n\n\\appendix\n\n" + pilot)


def typeset(tex, final):
    # Number the displayed equations so a reviewer can cite one, and set
    # displays that follow each other with nothing between them as one group.
    # The body may not contain the closing delimiter, so a match never spans
    # the prose between two displays.
    body = r"(?:(?!\\\]).)*"
    display = re.compile(r"\\\[(" + body + r")\\\]", re.S)
    run = re.compile(r"(?:\\\[" + body + r"\\\][ \t]*\n?(?:[ \t]*\n)?){2,}", re.S)
    before = display.sub("<DISPLAY>", tex)
    count = len(display.findall(tex))

    def gather(match):
        rows = [row.strip() for row in display.findall(match.group(0))]
        return "\\begin{gather}\n" + " \\\\\n".join(rows) + "\n\\end{gather}\n\n"

    tex = run.sub(gather, tex)
    tex = display.sub(lambda m: "\\begin{equation}\n" + m.group(1).strip() + "\n\\end{equation}", tex)

    # A display belongs to the sentence that introduces it. Left as a paragraph
    # of its own it gets an empty line above it, and a sentence that continues
    # after it would start a new paragraph.
    opening = r"\\begin\{(?:equation|gather)\}"
    closing = r"\\end\{(?:equation|gather)\}"
    tex = re.sub(r"([^\n])\n\n+(" + opening + ")", r"\1\n\2", tex)
    tex = re.sub(r"(" + closing + r")\n\n+(?=[a-z])", r"\1\n", tex)

    # Nothing outside the displays may change, and no display may be lost.
    env = re.compile(r"\\begin\{(equation|gather)\}(.*?)\\end\{\1\}", re.S)
    rows_in = lambda m: 1 if m.group(1) == "equation" else m.group(2).count("\\\\") + 1
    if sum(rows_in(m) for m in env.finditer(tex)) != count:
        sys.exit("a displayed equation was lost while numbering")
    squash = lambda text: re.sub(r"\s+", " ", text)
    if squash(before) != squash(env.sub(lambda m: "\n\n".join(["<DISPLAY>"] * rows_in(m)), tex)):
        sys.exit("the prose changed while numbering equations")

    # Every table here is a few rows, so each is set as one block that cannot
    # break across pages. pandoc writes a longtable, which can: it split a
    # four-row table over two pages, and it starts a new page on its own terms,
    # which left a heading alone at the foot of the page before it.
    table = re.compile(
        r"\\begin\{longtable\}\[\]\{(?P<cols>.*?)\}\n\\toprule\\noalign\{\}\n(?P<head>.*?)"
        r"\\midrule\\noalign\{\}\n\\endhead\n\\bottomrule\\noalign\{\}\n\\endlastfoot\n"
        r"(?P<body>.*?)\\end\{longtable\}", re.S)

    def block(match):
        rows = match.group("body").count("\\\\\n")
        if rows > MAX_TABLE_ROWS:
            sys.exit(f"a table has {rows} rows; one that long has to be allowed to break across pages")
        # The \\nobreak keeps the block on the page of the line that introduces
        # it, so a page never ends on "was:" with the table overleaf.
        return ("\\par\\nobreak\\addvspace{\\medskipamount}\\noindent\\begin{minipage}{\\linewidth}\n"
                "\\begin{tabular}{" + match.group("cols") + "}\n\\toprule\\noalign{}\n" + match.group("head")
                + "\\midrule\\noalign{}\n" + match.group("body") + "\\bottomrule\\noalign{}\n"
                "\\end{tabular}\n\\end{minipage}\\par\\addvspace{\\medskipamount}")

    tables = tex.count("\\begin{longtable}")
    tex = table.sub(block, tex)
    if tex.count("\\begin{longtable}") != 0 or tex.count("\\begin{tabular}") != tables:
        sys.exit("a table was not in the shape pandoc usually writes, and was left as a longtable")

    # A section whose table would otherwise start on the next page moves there
    # whole, so its heading and lead-in stay with the table. \Needspace decides
    # the break at once. The lowercase form only makes an earlier break
    # cheaper, and TeX then settles it after the table has taken over the
    # output routine, which prints the table's header above the heading.
    heading = "\\section{Sample size}"
    if tex.count(heading) != 1:
        sys.exit("the Sample size section is missing or repeated")
    tex = tex.replace(heading, "\\Needspace{24\\baselineskip}\n" + heading)

    # The author is a group, so the title block carries its institute and its
    # website on the lines below the name.
    byline = "\\author{" + AUTHOR + "}"
    if tex.count(byline) != 1:
        sys.exit("the author line is missing")
    tex = tex.replace(byline, "\\author{" + AUTHOR + "\\\\[3pt]\n{\\normalsize " + INSTITUTE + "}\\\\[1pt]\n"
                      "{\\normalsize\\href{https://" + WEBSITE + "}{" + WEBSITE + "}}}")

    preamble = "\\usepackage{needspace}\n"
    if not final:
        # A light DRAFT mark behind the text of every page.
        preamble += "\\usepackage{draftwatermark}\n\\DraftwatermarkOptions{text=DRAFT, angle=45, color={[gray]{0.90}}}\n"
    tex = tex.replace("\\begin{document}", preamble + "\\begin{document}", 1)

    note = ("% Generated by tools/manuscript/build.py from docs/dispersion-model.md and\n"
            "% docs/repeat-runs-pilot.md. Edit those pages and rebuild; do not edit this file.\n")
    return note + tex


def main(argv):
    final = "--final" in argv[1:]
    for tool in ("pandoc", "tectonic"):
        if shutil.which(tool) is None:
            sys.exit(f"{tool} is not on PATH")

    with tempfile.TemporaryDirectory() as work:
        source = os.path.join(work, NAME + ".md")
        with open(source, "w", encoding="utf-8") as out:
            out.write(prepare())
        tex_path = os.path.join(work, NAME + ".tex")
        subprocess.run([
            "pandoc", source, "-o", tex_path, "--standalone",
            "--from", "markdown+tex_math_dollars+pipe_tables+raw_tex", "--shift-heading-level-by=-1",
            "--number-sections", "--pdf-engine=xelatex",
            "-M", "author=" + AUTHOR, "-M", "date=" + DATE,
            "-V", "documentclass=article", "-V", "papersize=a4", "-V", "fontsize=11pt",
            "-V", "geometry:margin=2.6cm", "-V", "linestretch=1.08",
        ], check=True)
        with open(tex_path, encoding="utf-8") as generated:
            tex = typeset(generated.read(), final)
        with open(tex_path, "w", encoding="utf-8") as out:
            out.write(tex)
        subprocess.run(["tectonic", "--keep-logs", tex_path], check=True, cwd=work)

        log = open(os.path.join(work, NAME + ".log"), encoding="utf-8", errors="replace").read()
        for problem in ("Missing character", "Overfull \\hbox"):
            if problem in log:
                print(f"warning: the LaTeX log reports {problem!r}", file=sys.stderr)
        for extension in (".tex", ".pdf"):
            shutil.copyfile(os.path.join(work, NAME + extension), os.path.join(ROOT, "docs", NAME + extension))
    print("wrote docs/" + NAME + ".tex and docs/" + NAME + ".pdf")


if __name__ == "__main__":
    main(sys.argv)
