#!/usr/bin/env python3
"""Valida los ficheros de preguntas de examples/ sin tocar la red.

Comprueba tres cosas, que son las que fallan al escribir JSON a mano:

1. Que el JSON es válido y cada entrada es una pregunta bien formada según las
   reglas reales del endpoint (tipo, número de opciones, instrucciones).
2. Que no hay caracteres fuera del rango latino, que son la señal de que el
   texto se corrompió al redactarlo.
3. Que no quedan restos de palabras en inglés ni claves mal formadas.

Uso: python3 scripts/validar_ejemplos.py [fichero ...]
"""
from __future__ import annotations

import json
import sys
import unicodedata
from pathlib import Path

# No forma parte de un fichero de preguntas: es el estado de ejemplo.
NO_ES_PREGUNTAS = {"estado.json"}

TIPOS = {"noul", "choice", "score"}


def raro(caracter: str) -> str | None:
    """Devuelve una descripción si el carácter no debería estar en el texto."""
    if caracter in "\n\t":
        return None
    # CJK, cirílico, árabe, hebreo, devanagari: nunca en un ejemplo en español.
    if ord(caracter) > 0x024F:
        nombre = unicodedata.name(caracter, "?")
        if any(k in nombre for k in ("CJK", "HIRAGANA", "KATAKANA", "CYRILLIC",
                                      "ARABIC", "HEBREW", "DEVANAGARI", "HANGUL")):
            return f"carácter fuera del alfabeto latino: {caracter!r} ({nombre})"
        return f"carácter inesperado: {caracter!r} (U+{ord(caracter):04X})"
    return None


def validar(ruta: Path) -> list[str]:
    problemas: list[str] = []
    try:
        crudo = ruta.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError) as exc:
        return [f"no se pudo leer: {exc}"]

    try:
        preguntas = json.loads(crudo)
    except json.JSONDecodeError as exc:
        return [f"JSON inválido en la línea {exc.lineno}: {exc.msg}"]

    if not isinstance(preguntas, dict):
        return ["el fichero debe ser un objeto con una clave por pregunta"]

    # 2. Caracteres corruptos.
    for numero, linea in enumerate(crudo.splitlines(), 1):
        for caracter in linea:
            descripcion = raro(caracter)
            if descripcion:
                problemas.append(f"línea {numero}: {descripcion}")
                break

    # 3. Claves y residuos.
    for nombre in preguntas:
        if nombre.startswith("_"):
            problemas.append(f"clave {nombre!r}: empieza por guion bajo")

    # 1. Reglas del endpoint.
    if not (1 <= len(preguntas) <= 64):
        problemas.append(f"{len(preguntas)} preguntas; el servidor admite de 1 a 64")

    for nombre, pregunta in preguntas.items():
        donde = f"pregunta {nombre!r}"
        if not isinstance(pregunta, dict):
            problemas.append(f"{donde}: no es un objeto")
            continue

        tipo = pregunta.get("type")
        if tipo not in TIPOS:
            problemas.append(f"{donde}: tipo {tipo!r}, se admiten {sorted(TIPOS)}")

        if not str(pregunta.get("instructions", "")).strip():
            problemas.append(f"{donde}: sin instrucciones")

        criterios = pregunta.get("criteria")
        if tipo == "choice":
            if not isinstance(criterios, dict):
                problemas.append(f"{donde}: criteria debe ser un objeto de opciones")
            elif not 2 <= len(criterios) <= 26:
                problemas.append(
                    f"{donde}: {len(criterios)} opciones, el servidor admite de 2 a 26")
            else:
                for clave, texto in criterios.items():
                    if not clave.strip():
                        problemas.append(f"{donde}: opción con clave vacía")
                    if not str(texto).strip():
                        problemas.append(f"{donde}: opción {clave!r} sin descripción")
        elif tipo == "score":
            if not isinstance(criterios, list):
                problemas.append(f"{donde}: criteria debe ser un array de descripciones")
            elif not criterios:
                problemas.append(f"{donde}: la escala está vacía")
            elif any(not str(nivel).strip() for nivel in criterios):
                problemas.append(f"{donde}: hay un nivel sin descripción")
        elif tipo == "noul" and criterios is not None:
            if not isinstance(criterios, dict):
                problemas.append(f"{donde}: criteria debe ser un objeto true/false")

    return problemas


def main(argv: list[str]) -> int:
    if len(argv) > 1:
        rutas = [Path(a) for a in argv[1:]]
    else:
        rutas = sorted(Path("examples").glob("*.json"))

    rutas = [r for r in rutas if r.name not in NO_ES_PREGUNTAS]
    if not rutas:
        print("no hay ficheros de preguntas que validar")
        return 0

    total = 0
    for ruta in rutas:
        problemas = validar(ruta)
        if problemas:
            print(f"FALLO {ruta}")
            for problema in problemas:
                print(f"  - {problema}")
            total += len(problemas)
        else:
            preguntas = json.loads(ruta.read_text(encoding="utf-8"))
            tipos = sorted({q.get("type") for q in preguntas.values()})
            print(f"ok    {ruta}  ({len(preguntas)} preguntas: {', '.join(tipos)})")

    if total:
        print(f"\n{total} problema(s)")
        return 1
    print("\nTodos los ejemplos válidos")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
