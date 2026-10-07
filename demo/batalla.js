/* Demo: el modelo juega a la batalla naval.
 *
 * Llama en directo a POST /v1/systemone de Ollama desde el navegador: sin
 * backend, sin paso de build y sin dependencias. La página solo aporta el
 * lienzo; aquí vive todo el estado del juego y toda la lógica.
 *
 * Formato de la llamada:
 *   {model, state:{mapa, impactos, disparos},
 *    questions:{casilla:{type:"choice", instructions, criteria}}}
 * y la respuesta aprovecha answers.casilla.{choice,probabilities}
 * junto con usage.input_tokens.
 */

"use strict";

/* ------------------------------------------------------------------------ *
 * 1. Tablero fijo (5×5) y barco
 * ------------------------------------------------------------------------ */

const FILAS = 5;
const COLUMNAS = 5;
const TAM_BARCOS = 3;
const TOTAL_CELDAS = FILAS * COLUMNAS;
const LIMITE_TURNO = TOTAL_CELDAS;

/* Claves de casilla en el mismo formato que el probe: "filacolumna" (00..44). */
const TODAS = [];
for (let f = 0; f < FILAS; f++) {
  for (let c = 0; c < COLUMNAS; c++) TODAS.push("" + f + c);
}

/* Texto exacto de los criterios: "choice" exige entre 2 y 26 entradas y el
 * servidor construye el prompt con estas descripciones. Con 25 casillas libres
 * como máximo, el límite de 26 se respeta. No se traducen ni se reordenan
 * porque cambian las probabilidades. */
function criterioDe(clave) {
  return "casilla fila " + clave[0] + " col " + clave[1];
}

const INSTRUCCIONES_BASE =
  "Batalla naval 5x5. Hay un barco de 3 casillas oculto. " +
  "Mapa (X=impacto, .=fallo, ?=sin disparar):\n{mapa}\n" +
  "Elige casilla sin disparar. Si hay X: el barco es contiguo, remata alrededor. " +
  "Si solo fallos: no repitas zonas ya exploradas; elige zona nueva.";

/* ------------------------------------------------------------------------ *
 * 2. Estado de la partida
 * ------------------------------------------------------------------------ */

let barco = [];               // 3 claves contiguas, horizontal o vertical
let impactos = new Set();     // casillas del barco ya alcanzadas
let vistos = new Map();       // clave -> "X" (impacto) | "." (fallo)
let disparos = 0;
let ultimaElegida = null;     // casilla elegida en el último turno del modelo
let calor = null;             // {probabilidades, max} del último turno
let fin = false;              // partida terminada (hundido o sin casillas)
let corriendo = false;        // bucle automático activo
let manual = false;           // modo «Yo disparo»
let token = 0;                // invalida los bucles anteriores al reiniciar
let ultimoTokens = null;
let ultimoMs = null;

/* Referencias a las celdas ya construidas. */
const celdas = [];

function generarBarco() {
  const horizontal = Math.random() < 0.5;
  const f = Math.floor(Math.random() * (horizontal ? FILAS : FILAS - TAM_BARCOS + 1));
  const c = Math.floor(Math.random() * (horizontal ? COLUMNAS - TAM_BARCOS + 1 : COLUMNAS));
  const piezas = [];
  for (let i = 0; i < TAM_BARCOS; i++) {
    piezas.push("" + (horizontal ? f : f + i) + (horizontal ? c + i : c));
  }
  return piezas;
}

function casillasLibres() {
  return TODAS.filter((k) => !vistos.has(k));
}

function mismaCelda(a, b) {
  return a !== null && a === b;
}

/* ------------------------------------------------------------------------ *
 * 3. Construcción del tablero en el DOM
 * ------------------------------------------------------------------------ */

function construirTablero() {
  const cont = document.getElementById("tablero");
  for (let f = 0; f < FILAS; f++) {
    celdas[f] = [];
    for (let c = 0; c < COLUMNAS; c++) {
      const clave = "" + f + c;
      const btn = document.createElement("button");
      btn.type = "button";
      btn.className = "celda";
      btn.dataset.clave = clave;
      btn.setAttribute("aria-label", "casilla fila " + f + " col " + c);
      btn.addEventListener("click", () => pulsarCelda(clave));
      cont.appendChild(btn);
      celdas[f][c] = btn;
    }
  }
}

/* Repinta el tablero completo: marcas de disparo, casilla elegida, mapa de
 * calor y revelado del barco al final. Es la misma función para el modelo y
 * para el modo manual, para que la comparación sea justa. */
function pintar() {
  for (const clave of TODAS) {
    const btn = celdas[+clave[0]][+clave[1]];
    const visto = vistos.get(clave);
    btn.classList.toggle("impacto", visto === "X");
    btn.classList.toggle("fallo", visto === ".");
    btn.classList.toggle("elegida", mismaCelda(clave, ultimaElegida));
    btn.classList.toggle("barco", fin && barco.includes(clave));
    btn.textContent = visto === "X" || (fin && barco.includes(clave)) ? "✖" : visto === "." ? "·" : "";

    /* Mapa de calor: intensidad proporcional a prob/max sobre las casillas
     * aún sin disparar; la casilla elegida queda resaltada con su contorno. */
    let fondo = "";
    let titulo = "";
    if (!fin && calor && !visto) {
      const p = Number(calor.probabilidades[clave]);
      if (Number.isFinite(p) && calor.max > 0) {
        const alfa = 0.12 + 0.68 * Math.max(0, Math.min(1, p / calor.max));
        fondo = "rgba(37, 99, 235, " + alfa.toFixed(3) + ")";
        titulo = (p * 100).toFixed(1).replace(".", ",") + " %";
      }
    }
    btn.style.backgroundColor = fondo;
    if (titulo) btn.title = titulo;
    else btn.removeAttribute("title");

    btn.disabled = fin || Boolean(visto);
  }
}

/* ------------------------------------------------------------------------ *
 * 4. Estado que ve el modelo
 * ------------------------------------------------------------------------ */

/* mapaEnTexto dibuja la rejilla en ASCII fila a fila con X, . y ?, porque un
 * modelo lee mucho mejor un mapa con símbolos que una lista de celdas. */
function mapaEnTexto() {
  const lineas = [];
  for (let f = 0; f < FILAS; f++) {
    const fila = [];
    for (let c = 0; c < COLUMNAS; c++) {
      const visto = vistos.get("" + f + c);
      fila.push(visto === "X" ? "X" : visto === "." ? "." : "?");
    }
    lineas.push(fila.join(" "));
  }
  return lineas.join("\n");
}

function construirEstado() {
  return {
    mapa: mapaEnTexto(),
    impactos: [...impactos].sort(),
    disparos: disparos,
  };
}

function construirPeticion() {
  const libres = casillasLibres();
  const criteria = {};
  for (const clave of libres) criteria[clave] = criterioDe(clave);
  return {
    model: nombreModelo(),
    state: construirEstado(),
    questions: {
      casilla: {
        type: "choice",
        instructions: INSTRUCCIONES_BASE.replace("{mapa}", mapaEnTexto()),
        criteria: criteria,
      },
    },
  };
}

/* ------------------------------------------------------------------------ *
 * 5. Llamada al servidor
 * ------------------------------------------------------------------------ */

class ErrorLlamada extends Error {
  constructor(titulo, detalle) {
    super(titulo);
    this.titulo = titulo;
    this.detalle = detalle || "";
  }
}

function recortar(texto) {
  const limpio = String(texto).replace(/\s+/g, " ").trim();
  return limpio.length > 400 ? limpio.slice(0, 400) + "…" : limpio;
}

function urlServidor() {
  const bruto = document.getElementById("url").value.trim();
  return bruto.replace(/\/+$/, "");
}

function nombreModelo() {
  return document.getElementById("modelo").value.trim() || "nimble";
}

/* Devuelve {elegida, probabilidades, tokens, ms} o lanza ErrorLlamada.
 * Todo fallo posible (red, HTTP, JSON, tipo inesperado) cae aquí para que la
 * interfaz muestre un aviso en español con el mensaje crudo del servidor. */
async function llamarModelo() {
  const t0 = performance.now();
  let res;
  try {
    res = await fetch(urlServidor() + "/v1/systemone", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(construirPeticion()),
    });
  } catch (e) {
    throw new ErrorLlamada(
      "No responde Ollama en " + (urlServidor() || "(URL vacía)") + " — ¿está arrancado?",
      e && e.message ? e.message : String(e),
    );
  }

  const texto = await res.text().catch(() => "");
  const ms = performance.now() - t0;

  let datos = null;
  try {
    datos = JSON.parse(texto);
  } catch {
    datos = null;
  }

  if (!res.ok) {
    const delServidor = datos && typeof datos.error === "string" ? datos.error : "";
    throw new ErrorLlamada(
      "El servidor respondió HTTP " + res.status + ".",
      delServidor || recortar(texto),
    );
  }
  if (datos === null) {
    throw new ErrorLlamada("La respuesta no es JSON válido.", recortar(texto));
  }

  const respuestas = datos.answers;
  const resp = respuestas && typeof respuestas === "object" ? respuestas.casilla : null;
  if (!resp || typeof resp !== "object") {
    throw new ErrorLlamada(
      "Falta la respuesta «casilla» en la respuesta del servidor.",
      recortar(texto),
    );
  }
  if (resp.type !== "choice") {
    throw new ErrorLlamada(
      "Tipo de respuesta inesperado: " + JSON.stringify(resp.type) + " (se esperaba «choice»).",
      recortar(texto),
    );
  }
  const libres = casillasLibres();
  if (typeof resp.choice !== "string" || !libres.includes(resp.choice)) {
    throw new ErrorLlamada(
      "Casilla inválida en la respuesta: " + JSON.stringify(resp.choice) + " (no está sin disparar).",
      recortar(texto),
    );
  }
  if (!resp.probabilities || typeof resp.probabilities !== "object") {
    throw new ErrorLlamada("La respuesta no incluye «probabilities».", recortar(texto));
  }

  const uso = datos.usage && typeof datos.usage === "object" ? datos.usage : {};
  return {
    elegida: resp.choice,
    probabilidades: resp.probabilities,
    tokens: Number.isFinite(uso.input_tokens) ? uso.input_tokens : null,
    ms,
  };
}

/* ------------------------------------------------------------------------ *
 * 6. Disparo
 * ------------------------------------------------------------------------ */

/* Aplica un disparo a una casilla. No hace fetch ni pinta: lo usan tanto el
 * modelo como el modo manual. Devuelve {impacto, hundido}. */
function aplicarDisparo(clave) {
  disparos++;
  vistos.set(clave, barco.includes(clave) ? "X" : ".");
  if (barco.includes(clave)) impactos.add(clave);
  return { impacto: impactos.has(clave), hundido: impactos.size === TAM_BARCOS };
}

function destellarHundido() {
  for (const clave of barco) {
    const btn = celdas[+clave[0]][+clave[1]];
    btn.classList.remove("hundido");
    void btn.offsetWidth; // reinicia la animación si se repite
    btn.classList.add("hundido");
  }
}

/* ------------------------------------------------------------------------ *
 * 7. Interfaz: barras, contador, registro, errores
 * ------------------------------------------------------------------------ */

const pct = (v) => (v * 100).toFixed(1).replace(".", ",") + " %";
const num2 = (v) => v.toFixed(2).replace(".", ",");

/* Las barras se reconstruyen en cada turno: las casillas libres cambian y el
 * límite de 26 criterios obliga a mostrar solo lo que aún se puede disparar. */
function pintarProbabilidades(res) {
  const cont = document.getElementById("probabilidades");
  cont.innerHTML = "";
  const libres = casillasLibres();
  const entradas = libres
    .map((clave) => {
      const v = Number(res.probabilidades[clave]);
      return { clave, p: Number.isFinite(v) ? Math.max(0, Math.min(1, v)) : 0 };
    })
    .sort((a, b) => b.p - a.p);

  for (const { clave, p } of entradas) {
    const fila = document.createElement("div");
    fila.className = "prob";
    if (mismaCelda(clave, res.elegida)) fila.classList.add("elegida");

    const nombre = document.createElement("span");
    nombre.className = "prob-nombre";
    nombre.textContent = clave;

    const barra = document.createElement("span");
    barra.className = "prob-barra";
    const relleno = document.createElement("span");
    relleno.className = "prob-relleno";
    relleno.style.width = (p * 100).toFixed(1) + "%";
    barra.appendChild(relleno);

    const valor = document.createElement("span");
    valor.className = "prob-valor";
    valor.textContent = pct(p);

    fila.append(nombre, barra, valor);
    cont.appendChild(fila);
  }
}

function limpiarProbabilidades() {
  document.getElementById("probabilidades").innerHTML = "";
}

function pintarContador() {
  document.getElementById("contador").textContent =
    "Disparos: " + disparos + " · Impactos: " + impactos.size + "/" + TAM_BARCOS;
}

function pintarEstado(texto, conMetricas) {
  const partes = [texto];
  if (conMetricas !== false) {
    if (ultimoTokens !== null) partes.push(ultimoTokens + " tokens de entrada");
    if (ultimoMs !== null)
      partes.push(Math.round(ultimoMs).toLocaleString("es-ES") + " ms (medido en el navegador)");
  }
  document.getElementById("estado").textContent = partes.join(" · ");
}

function registrar(texto, clase) {
  const lista = document.getElementById("registro");
  const vacio = lista.querySelector(".vacio");
  if (vacio) vacio.remove();
  const li = document.createElement("li");
  li.textContent = texto;
  if (clase) li.className = clase;
  lista.prepend(li);
  while (lista.children.length > 40) lista.lastElementChild.remove();
}

function mostrarError(titulo, detalle) {
  const caja = document.getElementById("error");
  caja.hidden = false;
  caja.querySelector(".error-titulo").textContent = titulo;
  caja.querySelector(".error-detalle").textContent = detalle ? "Detalle: " + detalle : "";
}

function limpiarError() {
  const caja = document.getElementById("error");
  caja.hidden = true;
  caja.querySelector(".error-titulo").textContent = "";
  caja.querySelector(".error-detalle").textContent = "";
}

function mostrarBanner(texto, limitado) {
  const b = document.getElementById("banner");
  b.textContent = texto;
  b.classList.toggle("limitado", Boolean(limitado));
  b.hidden = false;
}

function ocultarBanner() {
  document.getElementById("banner").hidden = true;
}

/* ------------------------------------------------------------------------ *
 * 8. Bucle de juego
 * ------------------------------------------------------------------------ */

function dormir(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function actualizarBotones() {
  const btnDisparar = document.getElementById("disparar");
  const btnManual = document.getElementById("manual");
  btnDisparar.textContent = corriendo ? "Pausa" : "Dispara el modelo";
  btnDisparar.disabled = manual || fin;
  btnManual.setAttribute("aria-pressed", manual ? "true" : "false");
}

function finalizar(texto, limitado) {
  fin = true;
  corriendo = false;
  calor = null;
  limpiarProbabilidades();
  mostrarBanner(texto, limitado);
  pintar();
  actualizarBotones();
}

/* Turno del modelo. Devuelve "ok" | "hundido" | "fallo" | "cancelado". */
async function turnoModelo(miToken) {
  let res;
  try {
    res = await llamarModelo();
  } catch (e) {
    // Si mientras esperábamos alguien reinició o pausó, la respuesta ya no
    // interesa: no se toca el tablero ni se muestra un error falso.
    if (miToken !== token) return "cancelado";
    if (e instanceof ErrorLlamada) mostrarError(e.titulo, e.detalle);
    else {
      const msg = e && e.message ? String(e.message) : String(e);
      mostrarError("Error inesperado al llamar al modelo.", msg);
    }
    return "fallo";
  }
  if (miToken !== token) return "cancelado";

  ultimoTokens = res.tokens;
  ultimoMs = res.ms;
  ultimaElegida = res.elegida;

  /* Mapa de calor: se calcula el máximo para normalizar la intensidad de las
   * casillas que quedan sin disparar. */
  let max = 0;
  for (const clave of Object.keys(res.probabilidades)) {
    const v = Number(res.probabilidades[clave]);
    if (Number.isFinite(v) && v > max) max = v;
  }
  calor = { probabilidades: res.probabilidades, max };

  pintarProbabilidades(res);

  const r = aplicarDisparo(res.elegida);
  const p = Number(res.probabilidades[res.elegida]);
  registrar(
    "T" + disparos + " · " + res.elegida + " → " +
      (Number.isFinite(p) ? pct(p) : "—") +
      (r.impacto ? " (¡impacto!)" : " (fallo)"),
    r.impacto ? "impacto" : "fallo",
  );

  pintar();
  pintarContador();

  if (r.hundido) {
    destellarHundido();
    finalizar("¡Barco hundido en " + disparos + " disparos!", false);
    return "hundido";
  }
  pintarEstado("Turno del modelo");
  return "ok";
}

async function bucleModelo() {
  const miToken = ++token;
  while (corriendo && miToken === token && !fin) {
    /* Con una sola casilla libre no hay nada que decidir: se dispara directamente
     * sin gastar una llamada al modelo (choice exige al menos 2 criterios). */
    const libres = casillasLibres();
    if (libres.length === 1) {
      ultimaElegida = libres[0];
      calor = null;
      limpiarProbabilidades();
      const r = aplicarDisparo(libres[0]);
      registrar(
        "T" + disparos + " · " + libres[0] + " → única casilla libre (sin llamada)",
        r.impacto ? "impacto" : "fallo",
      );
      pintar();
      pintarContador();
      if (r.hundido) {
        destellarHundido();
        finalizar("¡Barco hundido en " + disparos + " disparos!", false);
      } else {
        corriendo = false;
        actualizarBotones();
      }
      return;
    }

    const r = await turnoModelo(miToken);
    if (r === "cancelado" || r === "fallo") {
      corriendo = false;
      actualizarBotones();
      return;
    }
    if (r === "hundido") return;
    await dormir(Number(document.getElementById("velocidad").value) || 700);
  }
}

/* ------------------------------------------------------------------------ *
 * 9. Modo manual y controles
 * ------------------------------------------------------------------------ */

function pulsarCelda(clave) {
  if (!manual || fin || vistos.has(clave)) return;
  ultimaElegida = clave;
  calor = null;
  limpiarProbabilidades();
  const r = aplicarDisparo(clave);
  registrar(
    "M" + disparos + " · " + clave + " → " + (r.impacto ? "¡impacto!" : "fallo"),
    r.impacto ? "impacto" : "fallo",
  );
  pintar();
  pintarContador();
  if (r.hundido) {
    destellarHundido();
    finalizar("¡Barco hundido en " + disparos + " disparos! (tú)", false);
  } else {
    pintarEstado("Modo manual · sin llamada al modelo", false);
  }
}

function nuevaPartida() {
  token++; // mata cualquier bucle en vuelo
  corriendo = false;
  barco = generarBarco();
  impactos = new Set();
  vistos = new Map();
  disparos = 0;
  ultimaElegida = null;
  calor = null;
  fin = false;
  ultimoTokens = null;
  ultimoMs = null;

  limpiarError();
  ocultarBanner();
  limpiarProbabilidades();
  document.getElementById("registro").innerHTML =
    '<li class="vacio">Sin disparos todavía.</li>';
  pintarContador();
  pintarEstado(
    manual
      ? "Nueva partida. Modo manual: pulsa una casilla del tablero."
      : "Nueva partida. Pulsa «Dispara el modelo» para empezar.",
  );
  actualizarBotones();
  pintar();
}

function conectarControles() {
  document.getElementById("disparar").addEventListener("click", () => {
    if (manual || fin) return;
    corriendo = !corriendo;
    if (corriendo) {
      limpiarError();
      ocultarBanner();
      bucleModelo();
    }
    actualizarBotones();
  });

  document.getElementById("manual").addEventListener("click", () => {
    manual = !manual;
    token++;
    corriendo = false;
    if (manual) {
      calor = null;
      limpiarProbabilidades();
      pintar();
      pintarEstado("Modo manual activo: pulsa una casilla del tablero.", false);
    } else {
      pintarEstado("Modo manual desactivado. Pulsa «Dispara el modelo» para retomar.");
    }
    actualizarBotones();
  });

  document.getElementById("nueva").addEventListener("click", nuevaPartida);
}

/* ------------------------------------------------------------------------ *
 * 10. Arranque
 * ------------------------------------------------------------------------ */

function iniciar() {
  construirTablero();
  conectarControles();
  nuevaPartida();
}

iniciar();
