// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// The choice of where the next session runs and on what (048): the runtime,
// when the deployment offers more than one, then the Effort level, and on the
// Latere platform one specific model below the levels. One trigger on the
// papers card opens a panel; the panel holds the whole choice so Start stays
// one tap.
//
// It is built from the app's own primitives rather than a menu stack of its
// own: the trigger and the panel take the Select's look and its behavior
// (portalled, placed from the trigger, dismissed by a scroll or a click away,
// arrows to move, Enter to pick, Escape to close), and the runtimes are a
// Segmented.
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { createPortal } from "react-dom";
import type { ModelTier, RuntimeOption } from "./api";
import { countOf, type I18n } from "./i18n";
import { Ico } from "./icons";
import { formatUSD } from "./money";
import { Segmented } from "./ui";
import "./runtime-pick.css";

// Choice is what the next Start sends. tier is the stored preference (best,
// balanced, fast); model is one exact model as provider/id, and empty when the
// person picked a level.
export type Choice = { runtime: string; tier: string; model: string };

// The levels in the order every Latere picker shows them, cheapest first.
const levels = ["fast", "balanced", "best"] as const;

// runtimesFrom reads the server's answer. A deployment that predates runtime
// choice answers tiers only, which reads as one unnamed runtime.
export function runtimesFrom(answer: {
  tiers?: ModelTier[];
  runtimes?: RuntimeOption[];
}): RuntimeOption[] {
  if (answer.runtimes?.length) return answer.runtimes;
  return [
    {
      name: "",
      label: "",
      default: true,
      payer: "project",
      efforts: (answer.tiers ?? []).map((t) => ({
        tier: t.tier,
        effort: "",
        model: t.model,
        input: t.input,
        output: t.output,
        error: t.error,
      })),
    },
  ];
}

// A row the arrows move over: a level, or one specific model.
type Row = { kind: "level"; level: string } | { kind: "model"; model: string };

export function RuntimePick({
  i18n,
  runtimes,
  value,
  onChange,
}: {
  i18n: I18n;
  runtimes: RuntimeOption[];
  value: Choice;
  onChange: (c: Choice) => void;
}) {
  const { t } = i18n;
  const effortName = (tier: string) =>
    tier === "fast"
      ? t("effortQuick")
      : tier === "balanced"
        ? t("effortThorough")
        : t("effortSustained");
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [cursor, setCursor] = useState(0);
  const [at, setAt] = useState({ left: 0, top: 0, maxHeight: 0 });
  const btn = useRef<HTMLButtonElement>(null);
  const pop = useRef<HTMLDivElement>(null);
  const runtime =
    runtimes.find((r) => r.name === value.runtime) ??
    runtimes.find((r) => r.default) ??
    runtimes[0];
  const tier = value.tier || "best";

  const matches = useMemo(() => {
    const models = runtime?.models ?? [];
    const q = query.trim().toLowerCase();
    return (
      q ? models.filter((m) => m.toLowerCase().includes(q)) : models
    ).slice(0, 50);
  }, [runtime, query]);

  // The rows in the order they are drawn. A level the person cannot use now is
  // drawn, disabled, and skipped by the arrows.
  const rows = useMemo<Row[]>(
    () => [
      ...levels
        .filter(
          (level) => !runtime?.efforts.find((e) => e.tier === level)?.error,
        )
        .map((level) => ({ kind: "level" as const, level })),
      ...matches.map((model) => ({ kind: "model" as const, model })),
    ],
    [runtime, matches],
  );
  const chosenRow = rows.findIndex((r) =>
    value.model
      ? r.kind === "model" && r.model === value.model
      : r.kind === "level" && r.level === tier,
  );

  const close = useCallback((refocus = true) => {
    setOpen(false);
    setQuery("");
    if (refocus) btn.current?.focus();
  }, []);

  const show = () => {
    setCursor(Math.max(0, chosenRow));
    setOpen(true);
  };

  // Placed like the Select's list: under the trigger, aligned to its left edge
  // and kept on screen, and above it when there is no room below. The panel is
  // taller than any Select's list, so where neither side has room for all of
  // it, it opens toward the roomier side and scrolls inside that room.
  useLayoutEffect(() => {
    if (!open) return;
    const place = () => {
      const b = btn.current?.getBoundingClientRect();
      const p = pop.current;
      if (!b || !p) return;
      const width = p.offsetWidth;
      const height = p.scrollHeight;
      const below = window.innerHeight - b.bottom - 17;
      const above = b.top - 17;
      const up = height > below && above > below;
      const maxHeight = Math.max(160, up ? above : below);
      const top = up ? b.top - 5 - Math.min(height, maxHeight) : b.bottom + 5;
      const left = Math.max(
        12,
        Math.min(b.left, window.innerWidth - width - 12),
      );
      setAt({ left, top, maxHeight });
    };
    place();
    // A scroll of the page behind the panel moves the trigger out from under
    // it, so it closes. A scroll inside the panel is the reader using it: the
    // list of models scrolls, and so does the cursor's row coming into view.
    // The listener is in the capture phase so it hears every scroller, which
    // is why it has to ask where the scroll began; it used to close on all of
    // them, so the first wheel tick over the list of models shut the panel.
    const dismiss = (e: Event) => {
      if (e.target instanceof Node && pop.current?.contains(e.target)) return;
      close(false);
    };
    window.addEventListener("resize", place);
    window.addEventListener("scroll", dismiss, { capture: true });
    return () => {
      window.removeEventListener("resize", place);
      window.removeEventListener("scroll", dismiss, { capture: true });
    };
  }, [open, close, runtime?.name, value.model, query]);

  useEffect(() => {
    if (!open) return;
    const away = (e: MouseEvent) => {
      const target = e.target as Node;
      if (!btn.current?.contains(target) && !pop.current?.contains(target))
        close(false);
    };
    document.addEventListener("mousedown", away);
    return () => document.removeEventListener("mousedown", away);
  }, [open, close]);

  // A search narrows the list under the cursor, so the cursor stays on a row.
  useEffect(() => {
    setCursor((c) => Math.min(c, Math.max(0, rows.length - 1)));
  }, [rows.length]);

  // The row under the cursor stays in view as the arrows move it.
  useEffect(() => {
    if (!open) return;
    pop.current
      ?.querySelector(".sel-opt.cursor")
      ?.scrollIntoView({ block: "nearest" });
  }, [open, cursor]);

  if (!runtime) return null;

  const pick = (row: Row | undefined) => {
    if (!row) return;
    onChange(
      row.kind === "level"
        ? { runtime: runtime.name, tier: row.level, model: "" }
        : { runtime: runtime.name, tier, model: row.model },
    );
    close();
  };

  // The same keys as the Select, from the trigger and from the search box.
  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") {
      if (!open) return;
      e.preventDefault();
      e.stopPropagation();
      return close();
    }
    if (e.key === "Enter") {
      e.preventDefault();
      return open ? pick(rows[cursor]) : show();
    }
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      if (!open) return show();
      if (rows.length)
        setCursor(
          (c) =>
            (c + (e.key === "ArrowDown" ? 1 : rows.length - 1)) % rows.length,
        );
    }
  };

  const summary = [
    runtimes.length > 1 ? runtime.label : "",
    value.model || effortName(tier),
  ]
    .filter(Boolean)
    .join(" · ");

  const price = (input?: number, output?: number) =>
    input && output
      ? t("tierPrice", {
          in: formatUSD(i18n.lang, input),
          out: formatUSD(i18n.lang, output),
        })
      : "";

  const rowIndex = (row: Row) =>
    rows.findIndex((r) =>
      r.kind === "level" && row.kind === "level"
        ? r.level === row.level
        : r.kind === "model" && row.kind === "model" && r.model === row.model,
    );

  return (
    <div className="sel rp">
      <button
        ref={btn}
        type="button"
        className={"sel-btn rp-btn" + (open ? " open" : "")}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-label={t("runtimePickLabel", { choice: summary })}
        onClick={() => (open ? close() : show())}
        onKeyDown={onKey}
      >
        <span className="sel-value">{summary}</span>
        <Ico name="chevronDown" size={13} className="sel-caret" />
      </button>
      {open &&
        createPortal(
          <div
            ref={pop}
            className="sel-list rp-pop"
            role="dialog"
            aria-label={t("runtimePickTitle")}
            style={{
              left: at.left,
              top: at.top,
              maxHeight: at.maxHeight || undefined,
            }}
            onKeyDown={(e) => {
              if (e.key === "Escape") {
                e.stopPropagation();
                close();
              }
            }}
          >
            {runtimes.length > 1 && (
              <div className="rp-section">
                <div className="rp-head">{t("runsOn")}</div>
                <Segmented
                  value={runtime.name}
                  label={t("runsOn")}
                  options={runtimes.map((r) => ({
                    value: r.name,
                    label: r.label,
                  }))}
                  onChange={(name) => {
                    setQuery("");
                    setCursor(0);
                    onChange({ runtime: name, tier, model: "" });
                  }}
                />
              </div>
            )}
            <div className="rp-section">
              <div className="rp-head">{t("effort")}</div>
              <div
                className="rp-rows"
                role="radiogroup"
                aria-label={t("effort")}
              >
                {levels.map((level) => {
                  const offer = runtime.efforts.find((e) => e.tier === level);
                  const on = !value.model && tier === level;
                  const unavailable = !!offer?.error;
                  const index = rowIndex({ kind: "level", level });
                  const cost = price(offer?.input, offer?.output);
                  return (
                    <button
                      key={level}
                      type="button"
                      role="radio"
                      aria-checked={on}
                      disabled={unavailable}
                      className={
                        "sel-opt rp-level" +
                        (index === cursor ? " cursor" : "") +
                        (on ? " on" : "")
                      }
                      onMouseEnter={() => index >= 0 && setCursor(index)}
                      onClick={() => pick({ kind: "level", level })}
                    >
                      <span className="rp-level-name">{effortName(level)}</span>
                      {!unavailable && cost && (
                        <span className="rp-price">{cost}</span>
                      )}
                      <span className="rp-level-detail">
                        {unavailable
                          ? t("effortUnavailable")
                          : offer?.model && (
                              <span className="rp-id">{offer.model}</span>
                            )}
                      </span>
                      {on && (
                        <Ico
                          name="check"
                          size={13}
                          w={2.2}
                          className="sel-tick rp-tick"
                        />
                      )}
                    </button>
                  );
                })}
              </div>
            </div>
            {!!runtime.models?.length && (
              <div className="rp-section">
                <div className="rp-head">{t("specificModel")}</div>
                <input
                  className="text rp-search"
                  type="search"
                  value={query}
                  placeholder={countOf(t, "searchModels", runtime.models.length)}
                  aria-label={t("specificModel")}
                  onChange={(e) => setQuery(e.target.value)}
                  onKeyDown={onKey}
                  spellCheck={false}
                />
                <ul
                  className="rp-models"
                  role="listbox"
                  aria-label={t("specificModel")}
                >
                  {matches.length === 0 && (
                    <li className="rp-empty">{t("noModelMatch")}</li>
                  )}
                  {matches.map((m) => {
                    const index = rowIndex({ kind: "model", model: m });
                    return (
                      <li key={m}>
                        <button
                          type="button"
                          role="option"
                          aria-selected={m === value.model}
                          className={
                            "sel-opt rp-model" +
                            (index === cursor ? " cursor" : "") +
                            (m === value.model ? " on" : "")
                          }
                          onMouseEnter={() => setCursor(index)}
                          onClick={() => pick({ kind: "model", model: m })}
                        >
                          <span className="sel-value rp-id">{m}</span>
                          {m === value.model && (
                            <Ico
                              name="check"
                              size={13}
                              w={2.2}
                              className="sel-tick"
                            />
                          )}
                        </button>
                      </li>
                    );
                  })}
                </ul>
              </div>
            )}
            <p className="rp-note">
              {runtime.payer === "person"
                ? t("paidByPerson")
                : t("paidByProject")}
            </p>
          </div>,
          document.body,
        )}
    </div>
  );
}
