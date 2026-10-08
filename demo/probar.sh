#!/usr/bin/env bash
# Prueba la demo de batalla naval sin navegador ni Ollama.
#
#   ./demo/probar.sh
#
# Comprueba tres cosas:
#   1. Que el JavaScript de la demo compila (node --check).
#   2. Que la demo sigue sin dependencias: sin import/export/require, sin
#      recursos externos y sin package.json ni node_modules.
#   3. Que una partida completa funciona: demo/probar.js carga batalla.js
#      sobre un DOM mínimo y juega contra un servidor simulado.
#
# No sustituye a la prueba a mano con un modelo real: eso es ./demo/run.sh.
#
# Salida: una línea por comprobación y código de salida distinto de cero si
# alguna falla.

set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

fallos=0
ok()  { printf 'ok   %s\n' "$1"; }
mal() { printf '✗    %s\n' "$1"; fallos=$((fallos + 1)); }

if ! command -v node >/dev/null 2>&1; then
  echo "✗ Falta el comando «node» en el PATH: hace falta para probar la demo." >&2
  echo "  Instálalo desde https://nodejs.org y vuelve a lanzar este script." >&2
  exit 1
fi

# --- 1. Sintaxis -----------------------------------------------------------
if node --check "$DIR/batalla.js"; then
  ok "sintaxis de batalla.js"
else
  mal "batalla.js no compila con node --check"
fi

if bash -n "$DIR/run.sh"; then
  ok "sintaxis de run.sh"
else
  mal "run.sh tiene un error de sintaxis"
fi

# --- 2. Sin dependencias ---------------------------------------------------
if grep -nE '\b(import|export|require)\b' "$DIR/batalla.js" >/dev/null; then
  mal "batalla.js usa import/export/require; la demo va sin dependencias"
else
  ok "batalla.js no usa import/export/require"
fi

if grep -nE 'https?://' "$DIR/batalla.js" | grep -v 'localhost:11434' >/dev/null; then
  mal "batalla.js llama a una URL que no es el Ollama local"
else
  ok "batalla.js no llama a ninguna URL externa"
fi

if grep -nE 'https?://' "$DIR/batalla.html" | grep -vE 'localhost:(11434|8000)' >/dev/null; then
  mal "batalla.html referencia una URL externa"
else
  ok "batalla.html no referencia recursos externos"
fi

externos="$(grep -oE '(src|href)="[^"]+"' "$DIR/batalla.html" | grep -vE '^src="batalla\.js"$' || true)"
if [ -n "$externos" ]; then
  mal "batalla.html carga algo que no es su propio script: $externos"
else
  ok "batalla.html solo carga batalla.js"
fi

if [ -e "$DIR/package.json" ] || [ -e "$DIR/node_modules" ]; then
  mal "la demo arrastra package.json o node_modules"
else
  ok "sin package.json ni node_modules"
fi

# --- 3. Partida completa contra el servidor simulado ------------------------
echo
if node "$DIR/probar.js"; then
  ok "partida simulada"
else
  mal "la partida simulada falló"
fi

echo
if [ "$fallos" -eq 0 ]; then
  echo "Demo correcta. Para probarla a mano y con un modelo real: ./demo/run.sh"
  exit 0
fi

echo "$fallos comprobación(es) fallida(s)." >&2
exit 1
