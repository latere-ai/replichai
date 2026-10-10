# SPDX-FileCopyrightText: 2026 Latere AI
# SPDX-License-Identifier: AGPL-3.0-or-later

"""Measure how repeat runs of one paper differ.

Two subcommands, so the published runs are re-fetched rather than kept in the
repository:

    python3 -I tools/repeat-runs/analyze.py fetch DIR SLUG [SLUG ...]
    python3 -I tools/repeat-runs/analyze.py report DIR

fetch reads every repository in the owner's organization (open-hai unless
REPLICHAI_GITHUB_OWNER says otherwise) whose name starts with one of the paper
slugs, through the gh CLI, and saves the files a run publishes into
DIR/<repo>/. report prints the tables docs/repeat-runs-pilot.md quotes.

Only the standard library is used. Downloaded files are data: they are parsed
as JSON and never executed.
"""

import itertools
import json
import os
import re
import statistics
import subprocess
import sys
from collections import Counter, defaultdict

FILES = ("verdict.json", "instrument.json", "replication.json", "environment.json")
OWNER = os.environ.get("REPLICHAI_GITHUB_OWNER", "open-hai")


def gh(*args):
    """Runs gh and returns stdout, or None when the call fails."""
    done = subprocess.run(["gh", *args], capture_output=True, text=True, check=False)
    return done.stdout if done.returncode == 0 else None


def fetch(directory, slugs):
    listing = gh("repo", "list", OWNER, "--limit", "1000", "--json", "name,createdAt")
    if listing is None:
        sys.exit("gh repo list failed; is gh signed in with access to " + OWNER + "?")
    repos = [r for r in json.loads(listing) if any(r["name"].startswith(s) for s in slugs)]
    for repo in sorted(repos, key=lambda r: r["name"]):
        target = os.path.join(directory, repo["name"])
        os.makedirs(target, exist_ok=True)
        for name in FILES:
            body = gh("api", f"repos/{OWNER}/{repo['name']}/contents/{name}",
                      "-H", "Accept: application/vnd.github.raw")
            if body is not None:
                with open(os.path.join(target, name), "w", encoding="utf-8") as out:
                    out.write(body)
        commits = gh("api", f"repos/{OWNER}/{repo['name']}/commits?per_page=100", "--jq", "length")
        meta = {"created_at": repo["createdAt"], "commits": int(commits) if commits else 0}
        with open(os.path.join(target, "repo.json"), "w", encoding="utf-8") as out:
            json.dump(meta, out)
        print(repo["name"], meta["commits"], "commits", file=sys.stderr)


def load(path):
    """Returns the parsed JSON object at path, or None when absent or unreadable."""
    try:
        with open(path, encoding="utf-8") as f:
            value = json.load(f)
    except (OSError, ValueError):
        return None
    return value if isinstance(value, dict) else None


def system_of(manifest, env):
    """Names the agent system a run ran on.

    The manifest says so since it gained a run record. A run finalized before
    that says it only in its environment.json, and a run with neither is
    unrecorded.
    """
    run = (manifest or {}).get("run") or {}
    if run.get("harness") or run.get("model"):
        harness = run.get("harness") or run.get("runtime") or "?"
        return f"{harness} / {(run.get('model') or {}).get('id', '?')}"
    if not env:
        return "unrecorded"
    harness = (env.get("harness") or {}).get("name", "?")
    model = (env.get("model") or {}).get("id", "?")
    return f"{harness} / {model}"


def ending_of(manifest):
    """Says how a run ended, where its manifest records it."""
    run = (manifest or {}).get("run") or {}
    if not run.get("state"):
        return "not recorded"
    return f"{run['state']}: {run.get('reason') or 'no reason given'}"


def numbers(text):
    """The numbers in a reported value, normalized to three significant digits."""
    out = set()
    for token in re.findall(r"-?\d+\.\d+|-?\d{2,}", str(text)):
        value = abs(float(token))
        if 1900 <= value <= 2100 and float(token).is_integer():
            continue  # a year, not a result
        out.add(f"{value:.3g}")
    return out


def runs_in(directory):
    papers = defaultdict(list)
    for name in sorted(os.listdir(directory)):
        base = os.path.join(directory, name)
        if not os.path.isdir(base):
            continue
        verdict = load(os.path.join(base, "verdict.json"))
        manifest = load(os.path.join(base, "replication.json"))
        papers[name.rsplit("-", 1)[0]].append({
            "repo": name,
            "verdict": verdict if verdict and "boundary" in verdict else None,
            "instrument": load(os.path.join(base, "instrument.json")),
            "manifest": manifest,
            "system": system_of(manifest, load(os.path.join(base, "environment.json"))),
            "ending": ending_of(manifest),
            "meta": load(os.path.join(base, "repo.json")) or {},
        })
    return papers


def span(values):
    return f"{min(values)}-{max(values)}" if values else "n/a"


def sd(values):
    return statistics.stdev(values) if len(values) > 1 else float("nan")


def report(directory):
    papers = runs_in(directory)
    print("## Runs per paper\n")
    print("| paper | runs | with a verdict | digests |")
    print("|---|---|---|---|")
    for paper, runs in papers.items():
        digests = {((r["manifest"] or {}).get("reproduction") or {}).get("method_digest", "")[:15] for r in runs}
        digests.discard("")
        print(f"| {paper} | {len(runs)} | {sum(1 for r in runs if r['verdict'])} | {', '.join(sorted(digests)) or 'none'} |")

    print("\n## Runs with no verdict\n")
    print("| paper | repo | created | commits | how it ended |")
    print("|---|---|---|---|---|")
    for paper, runs in papers.items():
        for r in runs:
            if not r["verdict"]:
                print(f"| {paper} | {r['repo'][-4:]} | {r['meta'].get('created_at', '?')} | {r['meta'].get('commits', '?')} | {r['ending']} |")

    print("\n## Dispersion by agent system\n")
    print("| paper | system | n | inner components | verified rate mean | rate SD | rate SD, denominator fixed | decisions | outer rows | data found | schema accepted |")
    print("|---|---|---|---|---|---|---|---|---|---|---|")
    for paper, runs in papers.items():
        groups = defaultdict(list)
        for r in runs:
            if r["verdict"]:
                groups[r["system"]].append(r)
                groups["all systems"].append(r)
        for system in sorted(groups, key=lambda s: (s == "all systems", s)):
            group = groups[system]
            c = [g["verdict"]["components"] for g in group]
            totals = [x["total"] for x in c]
            rates = [x["verified"] / x["total"] for x in c if x["total"]]
            fixed = [x["verified"] / statistics.mean(totals) for x in c]
            decisions = [len(g["verdict"].get("decisions", [])) for g in group]
            outer = [sum(1 for b in g["verdict"]["boundary"] if b.get("loop") == "outer") for g in group]
            data = sum(1 for g in group for s in g["verdict"].get("scorecard", [])
                       if s.get("criterion") == "data" and s.get("found"))
            accepted = sum(1 for g in group if ((g["manifest"] or {}).get("findings") or {}).get("present"))
            print(f"| {paper} | {system} | {len(group)} | {span(totals)} | {statistics.mean(rates):.0%} | "
                  f"{sd(rates):.0%} | {sd(fixed):.0%} | {span(decisions)} | {span(outer)} | "
                  f"{data}/{len(group)} | {accepted}/{len(group)} |")

    print("\n## Within-paper variance of the component count, by system\n")
    print("| paper | system | n | variance |")
    print("|---|---|---|---|")
    pooled = defaultdict(list)
    for paper, runs in papers.items():
        groups = defaultdict(list)
        for r in runs:
            if r["verdict"]:
                groups[r["system"]].append(r["verdict"]["components"]["total"])
        for system, totals in sorted(groups.items()):
            if len(totals) > 1:
                pooled[system].append(statistics.variance(totals))
                print(f"| {paper} | {system} | {len(totals)} | {statistics.variance(totals):.1f} |")
    for system, variances in sorted(pooled.items()):
        print(f"| mean over papers | {system} | | {statistics.mean(variances):.1f} |")

    print("\n## The paper's reported results, as each run extracted them\n")
    print("| paper | runs with a list | entries per run | distinct numbers | in every run | in at least half | in one run only |")
    print("|---|---|---|---|---|---|---|")
    for paper, runs in papers.items():
        lists = []
        for r in runs:
            if not r["verdict"]:
                continue
            reported = (((r["instrument"] or {}).get("protocol") or {}).get("reported")) or []
            entries = [numbers(e.get("value", "")) for e in reported if isinstance(e, dict)]
            entries = [e for e in entries if e]
            lists.append(entries)
        with_list = [entries for entries in lists if entries]
        count = Counter(n for entries in with_list for n in set().union(*entries))
        k = len(with_list)
        print(f"| {paper} | {k} of {len(lists)} | {span([len(e) for e in with_list])} | {len(count)} | "
              f"{sum(1 for v in count.values() if v == k)} | {sum(1 for v in count.values() if 2 * v >= k)} | "
              f"{sum(1 for v in count.values() if v == 1)} |")

    print("\n## Pairs of runs: share of one run's reported entries with a number found in the other\n")
    for paper, runs in papers.items():
        lists = []
        for r in runs:
            reported = (((r["instrument"] or {}).get("protocol") or {}).get("reported")) or []
            entries = [numbers(e.get("value", "")) for e in reported if isinstance(e, dict)]
            entries = [e for e in entries if e]
            if r["verdict"] and entries:
                lists.append(entries)
        shares = []
        for a, b in itertools.permutations(lists, 2):
            pool = set().union(*b)
            shares.append(sum(1 for e in a if e & pool) / len(a))
        if shares:
            print(f"- {paper}: mean {statistics.mean(shares):.0%}, lowest {min(shares):.0%}")


def main(argv):
    if len(argv) >= 3 and argv[1] == "fetch":
        fetch(argv[2], argv[3:])
    elif len(argv) == 3 and argv[1] == "report":
        report(argv[2])
    else:
        sys.exit(__doc__)


if __name__ == "__main__":
    main(sys.argv)
