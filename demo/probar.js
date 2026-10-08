#!/usr/bin/env node
/*
 * Prueba la demo de batalla naval sin navegador.
 *
 * Levanta un servidor simulado que imita POST /v1/systemone, carga
 * demo/batalla.js sobre un DOM mínimo y juega una partida entera contra él.
 * Comprueba lo que el navegador no hace falta para verificar:
 *
 *   1. Cada petición respeta el contrato de la API: choice de 2 a 26
 *      criterios, exactamente una criteria por casilla libre, la rejilla en
 *      ASCII y las instrucciones de la partida.
 *   2. El mapa de calor y las barras de probabilidad se pintan sobre las
 *      casillas sin disparar.
 *   3. La partida termina con el barco hundido, el contador a 3/3 y sin
 *      ningún error en pantalla.
 *
 * No usa dependencias: solo módulos estándar de Node. Lo invoca
 * demo/probar.sh, que además valida la sintaxis y la ausencia de dependencias.
 *
 * Uso: node demo/probar.js
 */

"use strict";

const fs = require("node:fs");
const http = require("node:http");
const path = require("node:path");
const vm = require("node:vm");

const DIR = __dirname;
const FILAS = 5;
const COLUMNAS = 5;
const TOTAL_CELDAS = FILAS * COLUMNAS;

/* Peticiones recibidas por el servidor simulado: {cuerpo, problema}. */
const peticiones = [];

const fallos = [];
function comprobar(condicion, mensaje) {
  if (!condicion) fallos.push(mensaje);
}

/* ------------------------------------------------------------------------ *
 * DOM mínimo: lo justo para que batalla.js se ejecute sin navegador
 * ------------------------------------------------------------------------ */

class Nodo {
  constructor(etiqueta) {
    this.tagName = String(etiqueta || "").toUpperCase();
    this.children = [];
    this.parent = null;
    this.attrs = {};
    this.style = {};
    this.dataset = {};
    this._clases = new Set();
    this._texto = "";
    this._html = "";
    this._handlers = {};
    this.hidden = false;
    this.disabled = false;
    this.value = "";
    this.type = "";
    this.title = "";
    this.offsetWidth = 0;

    const self = this;
    this.classList = {
      add(...clases) {
        clases.forEach((c) => self._clases.add(String(c)));
      },
      remove(...clases) {
        clases.forEach((c) => self._clases.delete(String(c)));
      },
      contains(clase) {
        return self._clases.has(String(clase));
      },
      toggle(clase, forzar) {
        const activo = forzar === undefined ? !self._clases.has(String(clase)) : Boolean(forzar);
        if (activo) self._clases.add(String(clase));
        else self._clases.delete(String(clase));
        return activo;
      },
    };
  }

  get className() {
    return [...this._clases].join(" ");
  }

  set className(valor) {
    this._clases = new Set(String(valor).split(/\s+/).filter(Boolean));
  }

  get lastElementChild() {
    return this.children[this.children.length - 1] || null;
  }

  get textContent() {
    if (this.children.length) return this.children.map((hijo) => hijo.textContent).join("");
    return this._texto;
  }

  set textContent(valor) {
    this._texto = String(valor);
    this.children = [];
  }

  get innerHTML() {
    return this._html;
  }

  // Solo se usa con "" (vaciar) o con un único elemento como
  // <li class="vacio">texto</li>, así que no hace falta un parser de HTML.
  set innerHTML(valor) {
    this._html = String(valor);
    this.children = [];
    const html = this._html.trim();
    if (html === "") return;
    const coincidencia = html.match(/^<([a-zA-Z0-9]+)(?:\s+class="([^"]*)")?>([\s\S]*?)<\/\1>$/);
    if (!coincidencia) return;
    const hijo = new Nodo(coincidencia[1]);
    if (coincidencia[2]) hijo.className = coincidencia[2];
    hijo.textContent = coincidencia[3];
    this.appendChild(hijo);
  }

  appendChild(hijo) {
    hijo.parent = this;
    this.children.push(hijo);
    return hijo;
  }

  append(...hijos) {
    hijos.forEach((hijo) => this.appendChild(hijo));
  }

  prepend(hijo) {
    hijo.parent = this;
    this.children.unshift(hijo);
    return hijo;
  }

  remove() {
    if (this.parent) {
      this.parent.children = this.parent.children.filter((hijo) => hijo !== this);
    }
    this.parent = null;
  }

  setAttribute(clave, valor) {
    this.attrs[clave] = String(valor);
  }

  getAttribute(clave) {
    return Object.prototype.hasOwnProperty.call(this.attrs, clave) ? this.attrs[clave] : null;
  }

  removeAttribute(clave) {
    delete this.attrs[clave];
  }

  addEventListener(evento, manejador) {
    (this._handlers[evento] = this._handlers[evento] || []).push(manejador);
  }

  // Simula el clic del navegador invocando los manejadores registrados.
  click() {
    (this._handlers.click || []).forEach((manejador) => manejador({}));
  }

  querySelector(selector) {
    return buscar(this, selector);
  }
}

function coincide(nodo, selector) {
  if (selector.startsWith(".")) return nodo._clases.has(selector.slice(1));
  if (selector.startsWith("#")) return nodo.attrs.id === selector.slice(1);
  return nodo.tagName === selector.toUpperCase();
}

function buscar(nodo, selector) {
  for (const hijo of nodo.children) {
    if (coincide(hijo, selector)) return hijo;
    const anidado = buscar(hijo, selector);
    if (anidado) return anidado;
  }
  return null;
}

function crearDocumento() {
  const porId = new Map();
  return {
    getElementById: (id) => porId.get(id) || null,
    createElement: (etiqueta) => new Nodo(etiqueta),
    registrar(id, etiqueta) {
      const nodo = new Nodo(etiqueta);
      nodo.setAttribute("id", id);
      porId.set(id, nodo);
      return nodo;
    },
  };
}

/* ------------------------------------------------------------------------ *
 * Servidor simulado: imita POST /v1/systemone
 * ------------------------------------------------------------------------ */

// Convierte el mapa ASCII de la demo en la lista de casillas sin disparar.
function celdasLibres(mapa) {
  const libres = [];
  mapa.split("\n").forEach((fila, f) => {
    fila.split(" ").forEach((simbolo, c) => {
      if (simbolo === "?") libres.push("" + f + c);
    });
  });
  return libres;
}

// Devuelve "" si la petición respeta el contrato de la API, o el motivo.
function revisarPeticion(cuerpo) {
  if (!cuerpo || typeof cuerpo !== "object") return "el cuerpo no es un objeto JSON";
  if (cuerpo.model !== "nimble") return "model inesperado: " + JSON.stringify(cuerpo.model);

  const mapa = cuerpo.state && cuerpo.state.mapa;
  if (typeof mapa !== "string") return "falta state.mapa";
  const filas = mapa.split("\n");
  if (filas.length !== FILAS) return "el mapa no tiene " + FILAS + " filas";
  for (const fila of filas) {
    if (!/^[X.?]( [X.?]){4}$/.test(fila)) return "formato del mapa inesperado: " + JSON.stringify(fila);
  }

  const pregunta = cuerpo.questions && cuerpo.questions.casilla;
  if (!pregunta) return "falta questions.casilla";
  if (pregunta.type !== "choice") return "tipo de pregunta inesperado: " + JSON.stringify(pregunta.type);
  if (typeof pregunta.instructions !== "string" || !pregunta.instructions.includes("Batalla naval")) {
    return "faltan las instrucciones de la partida";
  }

  const criterios = pregunta.criteria;
  if (!criterios || typeof criterios !== "object") return "faltan criteria";
  const claves = Object.keys(criterios);
  if (claves.length < 2 || claves.length > 26) {
    return "criteria fuera de 2–26: " + claves.length;
  }
  const libres = celdasLibres(mapa);
  if (claves.length !== libres.length) {
    return "criteria (" + claves.length + ") no coincide con las casillas libres (" + libres.length + ")";
  }
  for (const clave of libres) {
    if (!Object.prototype.hasOwnProperty.call(criterios, clave)) {
      return "falta la casilla libre " + clave + " en los criterios";
    }
    const esperado = "casilla fila " + clave[0] + " col " + clave[1];
    if (criterios[clave] !== esperado) {
      return "descripción distinta para " + clave + ": " + JSON.stringify(criterios[clave]);
    }
  }
  return "";
}

function crearServidor() {
  return http.createServer((peticion, respuesta) => {
    let cuerpoCrudo = "";
    peticion.on("data", (trozo) => {
      cuerpoCrudo += trozo;
    });
    peticion.on("end", () => {
      if (peticion.method !== "POST" || peticion.url !== "/v1/systemone") {
        respuesta.writeHead(404, { "Content-Type": "application/json" });
        respuesta.end(JSON.stringify({ error: "ruta no simulada: " + peticion.url }));
        return;
      }

      let cuerpo = null;
      try {
        cuerpo = JSON.parse(cuerpoCrudo);
      } catch {
        cuerpo = null;
      }

      const problema = revisarPeticion(cuerpo);
      peticiones.push({ cuerpo, problema });
      if (problema) {
        respuesta.writeHead(400, { "Content-Type": "application/json" });
        respuesta.end(JSON.stringify({ error: "petición fuera del contrato: " + problema }));
        return;
      }

      // Estrategia del simulador: la primera casilla libre en orden de
      // lectura. Al no repetir ninguna, recorre el tablero y acaba hundiendo
      // el barco, que es justo lo que la partida debe hacer.
      const libres = celdasLibres(cuerpo.state.mapa);
      const elegida = libres[0];
      const probabilities = {};
      for (const clave of libres) {
        probabilities[clave] = clave === elegida ? 0.6 : 0.4 / Math.max(1, libres.length - 1);
      }

      respuesta.writeHead(200, { "Content-Type": "application/json" });
      respuesta.end(
        JSON.stringify({
          model: cuerpo.model,
          answers: {
            casilla: { type: "choice", choice: elegida, probabilities, confidence: 0.5 },
          },
          usage: { input_tokens: 320, output_tokens: 1 },
        }),
      );
    });
  });
}

/* ------------------------------------------------------------------------ *
 * Utilidades de la partida
 * ------------------------------------------------------------------------ */

async function esperar(condicion, milisegundos, descripcion) {
  const limite = Date.now() + milisegundos;
  while (Date.now() < limite) {
    if (condicion()) return;
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  throw new Error("tiempo agotado esperando " + descripcion);
}

const celdasDelTablero = (doc) => doc.getElementById("tablero").children;
const libresDelTablero = (doc) =>
  celdasDelTablero(doc).filter(
    (celda) => !celda.classList.contains("impacto") && !celda.classList.contains("fallo"),
  );
const disparadasDelTablero = (doc) =>
  celdasDelTablero(doc).filter(
    (celda) => celda.classList.contains("impacto") || celda.classList.contains("fallo"),
  );

/* ------------------------------------------------------------------------ *
 * Partida
 * ------------------------------------------------------------------------ */

async function partida(servidor, doc) {
  const { port } = servidor.address();

  const url = doc.registrar("url", "input");
  url.value = "http://127.0.0.1:" + port;
  const modelo = doc.registrar("modelo", "input");
  modelo.value = "nimble";
  const velocidad = doc.registrar("velocidad", "select");
  velocidad.value = "150";
  doc.registrar("disparar", "button");
  doc.registrar("manual", "button");
  doc.registrar("nueva", "button");
  doc.registrar("tablero", "div");
  doc.registrar("probabilidades", "div");
  doc.registrar("contador", "p");
  doc.registrar("estado", "p");
  doc.registrar("registro", "ol");

  // La caja de error ya trae sus dos nodos en el HTML real.
  const cajaError = doc.registrar("error", "div");
  cajaError.hidden = true;
  const titulo = new Nodo("strong");
  titulo.className = "error-titulo";
  cajaError.appendChild(titulo);
  const detalle = new Nodo("p");
  detalle.className = "error-detalle";
  cajaError.appendChild(detalle);

  const banner = doc.registrar("banner", "div");
  banner.hidden = true;

  const sandbox = {
    document: doc,
    fetch,
    // La demo duerme entre turnos; en la prueba basta con un tick.
    setTimeout: (fn, ms) => setTimeout(fn, Math.min(Number(ms) || 0, 5)),
    clearTimeout,
    console,
    performance: { now: () => Number(process.hrtime.bigint()) / 1e6 },
  };

  const codigo = fs.readFileSync(path.join(DIR, "batalla.js"), "utf8");
  vm.runInNewContext(codigo, sandbox, { filename: "batalla.js" });

  comprobar(celdasDelTablero(doc).length === TOTAL_CELDAS, "el tablero no tiene " + TOTAL_CELDAS + " casillas");

  doc.getElementById("disparar").click();

  // Primer turno: el mapa de calor y las barras deben aparecer.
  await esperar(
    () => celdasDelTablero(doc).some((celda) => Boolean(celda.style.backgroundColor)),
    10000,
    "el mapa de calor del primer turno",
  );
  // Se muestrean ahora porque al terminar la partida la lista se vacía.
  const barras = doc.getElementById("probabilidades").children.length;
  const libresMuestreo = libresDelTablero(doc).length;
  const conCalor = celdasDelTablero(doc).filter((celda) => Boolean(celda.style.backgroundColor)).length;
  const destacadas = doc
    .getElementById("probabilidades")
    .children.filter((fila) => fila.classList.contains("elegida"));
  const claveDestacada =
    destacadas.length === 1 && destacadas[0].children.length > 0 ? destacadas[0].children[0].textContent : "";

  // Final de la partida: el tablero queda bloqueado y el banner visible.
  await esperar(
    () => !banner.hidden && celdasDelTablero(doc).every((celda) => celda.disabled),
    60000,
    "el final de la partida",
  );

  comprobar(barras >= 2, "no se pintaron las barras de probabilidad");
  // Las barras describen la decisión, que incluía la casilla recién disparada,
  // así que pueden ser una más que las casillas que quedan libres.
  comprobar(
    barras >= libresMuestreo && barras <= libresMuestreo + 1,
    "las barras (" + barras + ") no cuadran con las casillas libres (" + libresMuestreo + ")",
  );
  comprobar(
    conCalor === libresMuestreo,
    "el mapa de calor cubre " + conCalor + " casillas y hay " + libresMuestreo + " libres",
  );
  comprobar(
    destacadas.length === 1,
    "las barras no destacan exactamente una casilla elegida (" + destacadas.length + ")",
  );
  comprobar(
    disparadasDelTablero(doc).some((celda) => celda.dataset.clave === claveDestacada),
    "las barras destacan una casilla que no se disparó: " + JSON.stringify(claveDestacada),
  );
  comprobar(
    doc.getElementById("probabilidades").children.length === 0,
    "las barras siguen visibles con la partida terminada",
  );

  // La partida termina al hundir el barco, no al agotar el tablero: puede
  // quedar casilla sin disparar, pero ninguna se puede seguir pulsando.
  comprobar(
    disparadasDelTablero(doc).length >= 3,
    "se disparó a menos de 3 casillas: " + disparadasDelTablero(doc).length,
  );
  comprobar(
    celdasDelTablero(doc).every((celda) => celda.disabled),
    "quedan casillas pulsables con la partida terminada",
  );
  comprobar(
    celdasDelTablero(doc).filter((celda) => celda.classList.contains("barco")).length === 3,
    "el barco revelado no ocupa 3 casillas",
  );
  comprobar(doc.getElementById("banner").textContent.includes("hundido"), "el banner no anuncia el hundimiento");
  comprobar(doc.getElementById("contador").textContent.includes("3/3"), "el contador no marca 3/3 impactos");
  comprobar(doc.getElementById("error").hidden === true, "la caja de error se mostró sin motivo");
  comprobar(
    doc.getElementById("estado").textContent.includes("tokens de entrada"),
    "el estado no muestra los tokens de entrada",
  );
  comprobar(
    doc.getElementById("registro").children.some((fila) => fila.classList.contains("impacto")),
    "el registro no anota ningún impacto",
  );

  comprobar(peticiones.length >= 3, "el modelo no llegó a jugar");
  comprobar(peticiones.length <= TOTAL_CELDAS - 1, "demasiadas llamadas al modelo: " + peticiones.length);
  const invalidas = peticiones.filter((p) => p.problema);
  comprobar(
    invalidas.length === 0,
    "alguna petición no cumple el contrato de la API: " + (invalidas[0] ? invalidas[0].problema : ""),
  );

  return { disparos: disparadasDelTablero(doc).length, llamadas: peticiones.length, barras };
}

async function main() {
  const servidor = crearServidor();
  await new Promise((resolve) => servidor.listen(0, "127.0.0.1", resolve));

  let resumen = null;
  try {
    resumen = await partida(servidor, crearDocumento());
  } catch (error) {
    fallos.push(error && error.message ? error.message : String(error));
  } finally {
    await new Promise((resolve) => servidor.close(resolve));
  }

  if (fallos.length === 0 && resumen) {
    console.log(
      "ok   partida simulada: " + resumen.disparos + " disparos, " + resumen.llamadas + " llamadas al modelo, barco hundido",
    );
    console.log("ok   contrato de la API en todas las peticiones (choice 2–26, una criteria por casilla libre)");
    console.log("ok   mapa de calor y " + resumen.barras + " barras de probabilidad pintados");
  } else {
    fallos.forEach((fallo) => console.log("✗    " + fallo));
  }
  return fallos.length === 0 ? 0 : 1;
}

main()
  .then((codigo) => process.exit(codigo))
  .catch((error) => {
    console.error("✗    error inesperado en la prueba: " + (error && error.stack ? error.stack : error));
    process.exit(1);
  });
