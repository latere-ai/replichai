// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// The choice of where the next session runs and on what: the runtime, when the
// deployment offers more than one, then the Effort level, and on the Latere
// platform one specific model below the levels. One trigger on the papers card
// opens a panel that holds the whole choice, so Start stays one tap.
//
// The panel is latere-ui's GlassPopover, which opens it in the top layer,
// places it against the trigger, keeps it there as the page scrolls, and
// closes it on a click away, on Escape, or once the trigger scrolls out of
// sight. What it holds is this file's: the arrows move a cursor over the
// levels and models, Enter picks, and the runtimes are a GlassSegmented.
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { GlassPopover, GlassSegmented } from "latere-ui/react";
import type { ModelTier, RuntimeOption } from "./api";
import { countOf, type I18n } from "./i18n";
import { Ico } from "./icons";
import { formatUSD } from "./money";
import "./runtime-pick.css";

// Choice is what the next Start sends. tier is the stored preference (best,
// balanced, fast); model is one exact model as provider/id, and empty when the
// person picked a level.
export type Choice = { runtime: string; tier: string; model: string };

// The levels in the order every Latere picker shows them, cheapest first.
const levels = ["fast", "balanced", "best"] as const;

// runtimesFrom reads the server's answer. An answer with tiers and no runtimes
// reads as one unnamed runtime.
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

  // A search narrows the list under the cursor, so the cursor stays on a row.
  useEffect(() => {
    setCursor((c) => Math.min(c, Math.max(0, rows.length - 1)));
  }, [rows.length]);

  // The row under the cursor stays in view as the arrows move it.
  useEffect(() => {
    if (!open) return;
    pop.current
      ?.querySelector(".rp-opt.cursor")
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

  // The keys of a select, from the trigger and from the search box. Escape is
  // the popover's: it closes the panel and gives the trigger its focus back.
  // A key handled here goes no further, so the popover does not act on it too.
  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === "Enter") {
      e.preventDefault();
      e.stopPropagation();
      return open ? pick(rows[cursor]) : show();
    }
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      e.stopPropagation();
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
    <GlassPopover
      className="rp"
      surface="solid"
      open={open}
      onOpenChange={(next) => (next ? show() : close(false))}
      trigger={({ open, id }) => (
        <button
          ref={btn}
          type="button"
          className={"rp-btn" + (open ? " open" : "")}
          aria-haspopup="dialog"
          aria-expanded={open}
          aria-controls={open ? id : undefined}
          aria-label={t("runtimePickLabel", { choice: summary })}
          onKeyDown={onKey}
        >
          <span className="rp-value">{summary}</span>
          <Ico name="chevronDown" size={13} className="rp-caret" />
        </button>
      )}
    >
      <div
        ref={pop}
        className="rp-pop"
        role="dialog"
        aria-label={t("runtimePickTitle")}
      >
        {runtimes.length > 1 && (
          <div className="rp-section">
            <div className="rp-head">{t("runsOn")}</div>
            <GlassSegmented
              value={runtime.name}
              ariaLabel={t("runsOn")}
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
                    "rp-opt rp-level" +
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
                      className="rp-tick"
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
                        "rp-opt rp-model" +
                        (index === cursor ? " cursor" : "") +
                        (m === value.model ? " on" : "")
                      }
                      onMouseEnter={() => setCursor(index)}
                      onClick={() => pick({ kind: "model", model: m })}
                    >
                      <span className="rp-value rp-id">{m}</span>
                      {m === value.model && (
                        <Ico
                          name="check"
                          size={13}
                          w={2.2}
                          className="rp-tick"
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
      </div>
    </GlassPopover>
  );
}
