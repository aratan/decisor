# Feature: Demo visual de decisor (batalla naval)

**Branch:** `demo/laberinto`
**Created:** 2026-10-07
**Scope change (2026-10-07):** laberinto descartado — nimble no navega el
laberinto solo (oscila sin solución en todas las variantes probadas; solo
llega si el cliente calcula BFS y se lo da hecho). Sustituido por batalla
naval 5×5, validada con sonda: modelo hunde el barco en media 17.5 disparos
(aleatorio: 20.2) y SIEMPRE devuelve probabilidades sobre todas las casillas
libres — que es justo lo que la demo debe enseñar.

## Objective

Let people SEE the model deciding: an HTML+JS-vanilla demo where `decisor`
(Ollama `/v1/systemone`) plays battleship 5×5 live, showing per-cell
probabilities as a heatmap over the grid.

## Why

README sells benchmarks and tables; a live game makes the utility obvious in
seconds. User request: "una demo para que la gente vea la utilidad del modelo
decisor". Laberinto descartado tras evidencia de simulación (ver Scope
change).

## Scope

- IN: `demo/` static app (vanilla JS, no build step, no dependencies),
  batalla naval 5×5 jugada por el modelo con mapa de calor de probabilidades;
  modo manual para comparación; demo README snippet (Spanish).
- OUT: no Go code changes, no MCP-UI, no server backend, no new Go deps
  (zero-dep rule untouched). Tetris descartado (pendiente de reevaluar).

## Constraints

- Vanilla HTML+JS only. Browser calls Ollama directly
  (`POST http://localhost:11434/v1/systemone`) — no proxy, no build tools.
- UI copy + comments in **Spanish** (matches CLI/README convention).
- Prompt budget: 8194 tokens/question, 64 KiB body, 1–64 questions; `choice`
  criteria 2–26 entries → grid 5×5 = 25 casillas máx. por turno (cabe);
  cuando quede 1 casilla libre, dispararla sin llamar al modelo.
- Demo files live in `demo/` — NOT `examples/`, so the root test
  `TestEjemplosDelRepositorio` does not validate them (by design; question
  shape validated at runtime by the browser against the live server).
- CORS: Ollama must allow the page origin (`OLLAMA_ORIGINS`); demo must be
  served from localhost (document exact command).
- Solo modelos Nimble/Tev admitidos por systemone (qwen3.8 rechazado 400).

## Delivery

- `delivery_strategy`: auto-chain (session preflight)
- Forecast: batalla naval ≈ 350–500 authored lines → single work unit.
- Chain strategy: not yet needed (ask if forecast exceeds budget).

## Checklist

- [x] T1 Batalla naval demo: `demo/batalla.html` + `demo/batalla.js` — barco
      aleatorio 3 casillas, turno del modelo con `choice` sobre casillas
      libres, heatmap de `probabilities` sobre el grid, impactos/fallos
      visibles, modo manual, errores server-down en español. Borrar
      `demo/laberinto.*` (sustituido). Route: delegated (writer, 2+ files).
      Checks: automatizados en `demo/probar.sh` + `demo/probar.js` (sintaxis,
      sin dependencias y partida completa contra un servidor simulado).
- [x] T2 Demo docs: short Spanish README section in `demo/README.md` (how to
      serve, CORS env var, pull nimble). Route: inline (mechanical).
- [x] T3 Work-unit commits (Conventional Commits, Spanish subject).

## Acceptance criteria

- `git status`: only `demo/` + docs touched; zero Go changes; zero deps.
- Batalla naval: el modelo elige casilla con `choice` (2–25 criterios);
  UI muestra mapa de calor de probabilidades; fallo de servidor → error
  claro en español, no stack trace.
- All CI checks still green (`gofmt -l .`, `go vet`, `go test -race -cover`).

## Progress

- [x] Feature doc created (pre-write gate).
- [x] Sondas: laberinto inviable (6 variantes), batalla naval viable
      (8/8 hundidos, media 17.5 vs 20.2 aleatorio).
- [x] T1 Demo terminada: `demo/batalla.html`, `demo/batalla.js` y
      `demo/run.sh`, que la arranca con el CORS correcto.
- [x] T2 Docs de la demo en `demo/README.md`, incluida la sección de
      comprobación.
- [x] T3 Commits por unidad de trabajo (Conventional Commits, asunto en
      español).
