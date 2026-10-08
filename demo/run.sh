#!/usr/bin/env bash
# Lanza la demo visual de decisor (batalla naval 5×5) y la deja lista para
# probar: comprueba Ollama y el modelo, levanta un servidor estático en la
# raíz del repo con el origen permitido por CORS y abre el navegador.
#
# Uso:
#   ./demo/run.sh                  # puerto 8000, modelo nimble
#   PORT=9000 ./demo/run.sh        # otro puerto
#   SIN_NAVEGADOR=1 ./demo/run.sh  # no abre el navegador
#
# Ctrl+C para el servidor estático y, si lo arrancó este script, también Ollama.

set -euo pipefail

PUERTO="${PORT:-8000}"
MODELO="${MODELO:-nimble}"
OLLAMA_URL="${OLLAMA_URL:-http://localhost:11434}"

DIR_DEMO="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RAIZ="$(dirname "$DIR_DEMO")"

PID_OLLAMA=""
PID_WEB=""
LOG_OLLAMA=""
LOG_WEB=""

limpiar() {
  trap - EXIT INT TERM
  if [ -n "$PID_WEB" ] && kill -0 "$PID_WEB" 2>/dev/null; then
    kill "$PID_WEB" 2>/dev/null || true
  fi
  if [ -n "$PID_OLLAMA" ] && kill -0 "$PID_OLLAMA" 2>/dev/null; then
    echo "· Paro el Ollama que había arrancado este script."
    kill "$PID_OLLAMA" 2>/dev/null || true
  fi
  if [ -n "$LOG_WEB" ]; then rm -f "$LOG_WEB"; fi
  if [ -n "$LOG_OLLAMA" ]; then rm -f "$LOG_OLLAMA"; fi
  echo "· Demo parada."
}

# Puerto libre: un connect que funciona significa que ya hay alguien escuchando.
puerto_ocupado() {
  (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null
}

ollama_activo() {
  curl -sf -o /dev/null "$OLLAMA_URL/api/version" 2>/dev/null
}

# Ollama escribe el nombre completo ("nimble:latest"); acepta las dos formas.
modelo_instalado() {
  ollama list 2>/dev/null | awk 'NR > 1 { print $1 }' | grep -qE "^${MODELO}(:.*)?$"
}

abrir_navegador() {
  case "$(uname -s)" in
    Darwin) open "$1" >/dev/null 2>&1 || true ;;
    MINGW*|MSYS*|CYGWIN*) start "" "$1" >/dev/null 2>&1 || true ;;
    *) xdg-open "$1" >/dev/null 2>&1 || true ;;
  esac
}

for comando in ollama python3 curl; do
  if ! command -v "$comando" >/dev/null 2>&1; then
    echo "✗ Falta el comando «$comando» en el PATH." >&2
    echo "  Ollama se instala desde https://ollama.com/download; python3 y curl" >&2
    echo "  suelen venir con el sistema." >&2
    exit 1
  fi
done

# El puerto se decide antes de arrancar Ollama: el origen CORS depende de él.
PUERTO_INICIAL="$PUERTO"
while puerto_ocupado "$PUERTO"; do
  PUERTO=$((PUERTO + 1))
  if [ "$PUERTO" -gt "$((PUERTO_INICIAL + 20))" ]; then
    echo "✗ No encuentro un puerto libre a partir del $PUERTO_INICIAL." >&2
    exit 1
  fi
done
if [ "$PUERTO" != "$PUERTO_INICIAL" ]; then
  echo "· El puerto $PUERTO_INICIAL estaba ocupado; uso el $PUERTO."
fi

ORIGEN="http://localhost:$PUERTO"
ORIGEN_ALT="http://127.0.0.1:$PUERTO"
PAGINA="$ORIGEN/demo/batalla.html"

trap limpiar EXIT INT TERM

# --- Ollama: en marcha, o arrancado aquí con el origen de la demo permitido ---
if ollama_activo; then
  echo "· Ollama ya está en marcha en $OLLAMA_URL."
else
  echo "· Arranco Ollama con OLLAMA_ORIGINS=$ORIGEN …"
  LOG_OLLAMA="$(mktemp -t decisor-ollama.XXXXXX)"
  OLLAMA_ORIGINS="$ORIGEN,$ORIGEN_ALT" ollama serve >"$LOG_OLLAMA" 2>&1 &
  PID_OLLAMA=$!

  listo=0
  for _ in $(seq 1 60); do
    if ollama_activo; then listo=1; break; fi
    if ! kill -0 "$PID_OLLAMA" 2>/dev/null; then
      echo "✗ Ollama no ha arrancado. Últimas líneas del log:" >&2
      tail -n 20 "$LOG_OLLAMA" >&2 || true
      exit 1
    fi
    sleep 0.5
  done
  if [ "$listo" -ne 1 ]; then
    echo "✗ Ollama no responde en $OLLAMA_URL tras 30 s. Log:" >&2
    tail -n 20 "$LOG_OLLAMA" >&2 || true
    exit 1
  fi
fi

# --- Modelo: solo Nimble o Tev aceptan /v1/systemone ---
if ! modelo_instalado; then
  echo "· Falta el modelo «$MODELO» (solo los modelos Nimble o Tev admiten /v1/systemone)."
  if [ -t 0 ]; then
    read -r -p "¿Lo descargo ahora con «ollama pull $MODELO»? [s/N] " respuesta
    case "$respuesta" in
      s|S|si|sí|y|Y) ollama pull "$MODELO" ;;
      *)
        echo "  Ejecuta «ollama pull $MODELO» y vuelve a lanzar este script."
        exit 1
        ;;
    esac
  else
    echo "  Ejecuta «ollama pull $MODELO» y vuelve a lanzar este script." >&2
    exit 1
  fi
fi

# --- Servidor estático en la raíz del repo (la página vive en /demo) ---
LOG_WEB="$(mktemp -t decisor-web.XXXXXX)"
python3 -m http.server "$PUERTO" --bind 127.0.0.1 --directory "$RAIZ" >"$LOG_WEB" 2>&1 &
PID_WEB=$!

listo=0
for _ in $(seq 1 40); do
  if curl -sf -o /dev/null "$PAGINA"; then listo=1; break; fi
  if ! kill -0 "$PID_WEB" 2>/dev/null; then
    echo "✗ El servidor estático no ha arrancado:" >&2
    tail -n 20 "$LOG_WEB" >&2 || true
    exit 1
  fi
  sleep 0.25
done
if [ "$listo" -ne 1 ]; then
  echo "✗ El servidor estático no responde en $PAGINA. Log:" >&2
  tail -n 20 "$LOG_WEB" >&2 || true
  exit 1
fi

# Aviso, no error: si Ollama ya venía de antes puede no incluir nuestro origen.
if ! curl -s -o /dev/null -D - -X OPTIONS "$OLLAMA_URL/v1/systemone" \
  -H "Origin: $ORIGEN" \
  -H "Access-Control-Request-Method: POST" \
  -H "Access-Control-Request-Headers: content-type" 2>/dev/null |
  grep -qi '^access-control-allow-origin:'; then
  echo "⚠ Ollama no parece permitir el origen $ORIGEN (CORS)."
  echo "  Reinícialo con: OLLAMA_ORIGINS=$ORIGEN,$ORIGEN_ALT ollama serve"
fi

echo
echo "  Demo lista → $PAGINA"
echo "  Modelo: $MODELO · Ollama: $OLLAMA_URL"
echo "  Pulsa «Dispara el modelo» y verás el mapa de calor de probabilidades."
echo "  (Ctrl+C para parar)"
echo

if [ "${SIN_NAVEGADOR:-0}" != "1" ]; then
  abrir_navegador "$PAGINA"
fi

wait "$PID_WEB"
