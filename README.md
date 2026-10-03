<p align="center">
  <img src="assets/logo.svg" alt="decisor" width="104">
</p>

# decisor

<p align="center">
  <em>Hazle preguntas a un modelo y obtén <strong>probabilidades</strong>, no prosa.<br>
  Cliente Go del endpoint de decisiones de Ollama, <code>/v1/systemone</code>.<br>
  <strong>Todo en local: sin cuenta, sin clave de API y sin coste por token.</strong></em>
</p>

<p align="center">
  <a href="https://github.com/aratan/decisor/actions/workflows/ci.yml"><img src="https://github.com/aratan/decisor/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <img src="https://img.shields.io/badge/Go-1.24-00ADD8?logo=go&logoColor=white" alt="Go 1.24">
  <img src="https://img.shields.io/badge/Licencia-MIT-yellow.svg" alt="Licencia MIT">
  <img src="https://img.shields.io/badge/dependencias-0-success" alt="Sin dependencias">
</p>

---

Un LLM al que le haces una pregunta te responde con una frase, y para decidir
automáticamente tienes que interpretarla. `decisor` va al revés: defines las
respuestas posibles y el modelo devuelve la **probabilidad de cada una**.

```
PREGUNTA       TIPO    RESPUESTA    CONFIANZA
is_question    noul    no           0.03
politeness     score   educado (1)  0.42
says_greeting  noul    sí           0.99
tone           choice  greeting     0.73
```

En una sola llamada, contra un texto largo, en milisegundos. Sin parsear frases,
sin regex y sin inventar respuestas que no le hayas dado tú.

**Introdúcelo con cuidado**, porque el resultado depende de dos cosas que no
son obvias y que están documentadas más abajo: **el nombre de cada pregunta
entra en el prompt** (renombrarla cambia la probabilidad) y las preguntas se
califican **de forma independiente**, sin razonar entre ellas.

## Índice

- [Por qué](#por-qué)
- [Coste](#coste)
- [Requisitos](#requisitos)
- [Instalación](#instalación)
- [Uso rápido](#uso-rápido)
- [Referencia de la CLI](#referencia-de-la-cli)
- [El archivo de preguntas](#el-archivo-de-preguntas)
- [Salida estructurada](#salida-estructurada)
- [Uso como biblioteca](#uso-como-biblioteca)
- [Límites del servidor](#límites-del-servidor)
- [Cómo interpretar los resultados](#cómo-interpretar-los-resultados)
- [Rendimiento](#rendimiento)
- [Desarrollo](#desarrollo)
- [Licencia](#licencia)

## Por qué

- **Respuestas acotadas.** El modelo elige entre lo que tú le das. Nunca inventa.
- **Probabilidades, no etiquetas.** Cada opción viene con su probabilidad, así
  que puedes fijar un umbral en vez de aceptar lo primero que salga.
- **Hasta 64 preguntas por llamada.** El estado se procesa una vez, así que
  preguntar más sale casi gratis ([medido](#rendimiento)).
- **Validación local.** Las preguntas se comprueban antes de salir a la red; los
  errores de esquema son un `ErrInvalidRequest`, no un HTTP 400.
- **Coste cero por token.** Sin cuota, sin factura y sin depender de un proveedor externo.
- **Cero dependencias.** Solo la biblioteca estándar de Go.

El alcance es deliberadamente estrecho: solo `POST /v1/systemone`. No hay cliente
de chat, porque no es lo que este tipo de modelo hace bien.

## Coste

**Cero.** `decisor` habla con Ollama en `localhost:11434` y, por defecto, no sale
a internet. No hay cuenta que abrir, ni clave de API, ni factura por token:

- **Sin coste por token.** El modelo se carga en tu máquina y se reutiliza
  entre llamadas.
- **Sin cuotas.** No hay tope de llamadas ni peticiones por minuto.
- **Los datos no se van.** El texto que evalúas no sale del equipo, lo cual suele
  ser lo que bloquea estos usos en entornos con datos sensibles.

Esto es justo lo que lo diferencia de un modelo de pago: aquí decides tú
cuántas preguntas se hacen, y puedes hacerlas muchas.

Lo único que pagas es el hardware. Si ya tienes un equipo, el coste es cero; si
alquilas una GPU en vez de usar tu propio ordenador, claramente deja de serlo.
"Cero coste" presupone hardware propio. Lo único que hace falta es que quepa el
modelo: Nimble en Q8\_0 pesa 9,5 GB, así que conviene un equipo con unos 10 GB
de RAM disponibles, o cuantizarlo a menos.

Las licencias tampoco ponen restricciones: `decisor` es [MIT](LICENSE) y
[Nimble](https://huggingface.co/Bespoke-Nimble-9B) es Apache 2.0 de
[Bespoke Labs](https://www.bespokelabs.ai), así que ambos se pueden usar en
proyectos comerciales sin pedir permiso.

## Requisitos

- Go 1.24 o superior (para compilar).
- Un servidor [Ollama](https://ollama.com) con un modelo de capacidad de
  decisión, por ejemplo [Nimble](https://ollama.com/library/nimble):

  ```bash
  ollama pull nimble
  ```

El endpoint `/v1/systemone` solo existe en builds de Ollama que anuncien la
capacidad `decision`. Compruébalo con:

```bash
curl -s localhost:11434/api/tags | grep -o '"capabilities":\[[^]]*\]'
```

## Instalación

```bash
# Con Go
go install github.com/aratan/decisor/cmd/decisor@latest

# O desde el código
git clone https://github.com/aratan/decisor.git
cd decisor
go build -o decisor ./cmd/decisor
```

## Uso rápido

```bash
# ¿Qué modelos hay?
decisor modelos

# Una decisión
decisor preguntar \
  --estado "Hola, ¿cómo estás? Me llamo Ana." \
  --preguntas examples/preguntas.json
```

El estado también puede ser un documento JSON, útil cuando el texto viene de
otra parte de tu sistema:

```bash
decisor preguntar \
  --estado-archivo examples/estado.json \
  --preguntas examples/preguntas.json
```

Y si solo necesitas un sí o un no, la pregunta más corta posible:

```bash
decisor preguntar \
  --estado "Hola, me llamo Ana" \
  --preguntas '{"saludo": {"type": "noul", "instructions": "¿El texto contiene un saludo?"}}'
```

Con `-` el estado se lee de la entrada estándar, para encadenarlo con cualquier
otra herramienta sin escribir un fichero temporal:

```bash
curl -s https://titulares.com/rss \
  | xmllint --xpath '//title/text()' - \
  | decisor preguntar --estado - --preguntas examples/mercados.json --json
```

### Ejemplos incluidos

Cinco conjuntos de preguntas listos para usar, todos de clasificación de texto,
que es lo que este tipo de modelo hace bien:

| Fichero | Para qué |
|---|---|
| [`examples/preguntas.json`](examples/preguntas.json) | Saludo, pregunta, tono y cortesía. El punto de partida. |
| [`examples/viajes.json`](examples/viajes.json) | Enrutar un mensaje de reserva: intención, urgencia, destino. |
| [`examples/coches.json`](examples/coches.json) | Clasificar un anuncio de coche usado y sus señales de riesgo. |
| [`examples/pisos.json`](examples/pisos.json) | Anuncio inmobiliario: venta o alquiler, distribución, precio. |
| [`examples/mercados.json`](examples/mercados.json) | Sentimiento de un titular de noticias. |

`examples/mercados.json` clasifica **texto**, no calcula nada: el modelo no
calcula un RSI ni una media móvil, elige entre las etiquetas que le das. Úsalo
como clasificador de titulares, no como ayuda a decidir una operación.

Para comprobar que los ejemplos siguen siendo válidos:

```bash
go test .
```

Ese test recorre `examples/` y valida cada fichero con `ParseQuestions`, que es
el mismo validador que usa la CLI. También busca caracteres de alfabetos que no
deberían aparecer en un ejemplo en español, que es la señal de que el texto se
corrompió al escribirlo.

## Referencia de la CLI

```
decisor modelos
decisor preguntar --estado "texto" --preguntas preguntas.json
```

| Subcomando | Qué hace |
|---|---|
| `modelos` | Lista los modelos disponibles. |
| `preguntar` | Evalúa un estado contra un conjunto de preguntas. |

### `preguntar`

| Flag | Por defecto | Qué hace |
|---|---|---|
| `--estado` | — | Texto a evaluar. `-` lo lee de la entrada estándar. |
| `--estado-archivo` | — | Documento JSON a evaluar como estado estructurado. |
| `--preguntas` | — | Fichero JSON con las preguntas (obligatorio). |
| `--model` | `nimble` | Modelo a usar. |
| `--url` | `http://localhost:11434` | Dirección del servidor. |
| `--umbral` | `0.5` | Probabilidad a partir de la cual un `noul` responde «sí». |
| `--sistema` | — | Instrucciones de sistema para el clasificador. |
| `--mantener` | — | Cuánto mantener el modelo cargado, p. ej. `5m`. |
| `--temperatura` | `0` | Temperatura de muestreo; `0` usa la del servidor. |
| `--seed` | `0` | Semilla de muestreo; `0` usa la del servidor. |
| `--json` | `false` | Escribe la respuesta estructurada en JSON por stdout. |
| `--salida` | — | Guarda además esa respuesta en JSON en el fichero indicado. |

Combinados, `--json` y `--salida` escriben el JSON en ambos destinos y no
imprimen el resumen legible.

### Tiempos

```
Tokens: 1553 de entrada, 5 de salida   Duración: 1.52 s (380 ms por pregunta)
```

## El archivo de preguntas

Su contenido es exactamente el valor del campo `questions` de la API, con una
clave por pregunta:

```json
{
  "says_greeting": {
    "type": "noul",
    "instructions": "¿El texto del estado contiene un saludo?"
  },
  "tone": {
    "type": "choice",
    "instructions": "¿Cuál es el tono del texto del estado?",
    "criteria": {
      "greeting": "El texto es un saludo.",
      "farewell": "El texto es una despedida.",
      "other": "El texto no es ni un saludo ni una despedida."
    }
  },
  "politeness": {
    "type": "score",
    "instructions": "¿Qué tan educado es el texto del estado?",
    "criteria": ["descortés", "educado", "muy educado"]
  }
}
```

### Los tres tipos

| Tipo | `criteria` | Qué devuelve |
|---|---|---|
| `noul` | **opcional**. `{"true": "...", "false": "..."}` para describir cada lado | `noul`: probabilidad de que sea cierto |
| `choice` | **obligatorio**. `{"clave": "descripción"}`, de 2 a 26 opciones | `choice` + `probabilities` + `confidence` |
| `score` | **obligatorio**. Serie de descripciones, la más baja primero, numeradas desde 0 | `score` (nivel ponderado) + `legend` + `probabilities` + `confidence` |

Dos detalles que no se deducen de la documentación del modelo y que están
verificados contra el servidor:

- **Omitir `criteria` no es lo mismo que enviar `null`.** En una pregunta `noul`,
  omitirlo es válido y deja la decisión en las instrucciones, pero un `null`
  explícito se rechaza. El cliente lo resuelve siempre por omisión.
- El endpoint acepta `criteria: null` documentado para `choice`, pero este build
  lo rechaza con `criteria must contain 2–26 candidates`. `choice` y `score`
  necesitan `criteria` de verdad.

## Salida estructurada

```bash
decisor preguntar ... --json | jq '.answers.tone.probabilities'
decisor preguntar ... --salida decision.json
```

```json
{
  "model": "nimble",
  "answers": {
    "tone": {
      "type": "choice",
      "choice": "greeting",
      "probabilities": { "greeting": 0.93, "farewell": 0.05, "other": 0.02 },
      "confidence": 0.73
    }
  },
  "usage": { "input_tokens": 1553, "output_tokens": 5 },
  "timing": { "duration_ms": 1789.5, "per_question_ms": 447.375 },
  "order": ["is_question", "politeness", "says_greeting", "tone"]
}
```

`answers` mantiene la forma exacta de la API. `timing` y `order` los añade la
CLI: los tiempos los mide el cliente, no el servidor.

Para respuestas de tipo `noul` en JSON, recuerda que la clave es `noul`:

```bash
decisor preguntar ... --json | jq '.answers.says_greeting.noul'
```

## Uso como biblioteca

```go
import "github.com/aratan/decisor/ollama"

client := ollama.New() // http://localhost:11434 por defecto

greeting, err := ollama.NewNoulQuestion(
    "¿El texto del estado contiene un saludo?",
    ollama.NoulCriteria{
        True:  "El texto del estado contiene un saludo.",
        False: "El texto del estado no contiene un saludo.",
    },
)
if err != nil {
    return err
}

// El estado puede ser texto o un objeto/array JSON decodificado.
resp, err := client.Decide(ctx, ollama.DecideRequest{
    Model:     "nimble",
    State:     ollama.StringState("Hola, ¿cómo estás? Me llamo Ana."),
    Questions: map[string]ollama.Question{"saludo": greeting},
})
if err != nil {
    return err
}

// Las respuestas son una unión discriminada por `type`.
if noul, ok := resp.Answers["saludo"].AsNoul(); ok && noul.Verdict(0.5) {
    log.Printf("es un saludo con probabilidad %.2f", noul.Probability)
}

// Vienen en el mismo orden en que se enviaron las preguntas.
for _, named := range resp.AnswersInOrder() {
    fmt.Println(named.Name, named.Answer.Type)
}

log.Println("tardó", resp.Duration)
```

Para cargar preguntas desde configuración o un fichero, `ParseQuestions` valida
el conjunto entero y nombra en el error la pregunta que falla:

```go
// Desde un []byte o un io.Reader, con el mismo formato del campo "questions".
questions, err := ollama.LoadQuestions(file)
if err != nil {
    return err // p. ej. question "tono": 1 opciones, el servidor admite de 2 a 26
}

resp, err := client.Decide(ctx, ollama.DecideRequest{
    Model:     "nimble",
    State:     ollama.StringState("Hola, ¿cómo estás?"),
    Questions: questions,
})
```

Para el caso más común, un sí o un no, hay una llamada que evita montar el mapa
a mano:

```go
saludo, err := ollama.NewNoulQuestion("¿Contiene un saludo?", ollama.NoulCriteria{})
if err != nil {
    return err
}

answer, err := client.DecideYesNo(ctx, "nimble", ollama.StringState("Hola, soy Ana"), saludo)
if err != nil {
    return err
}
if noul, ok := answer.AsNoul(); ok && noul.Verdict(0.5) {
    fmt.Println("es un saludo")
}
```

### Errores

| Función | Significa |
|---|---|
| `errors.Is(err, ollama.ErrInvalidRequest)` | El cliente lo rechazó antes de la llamada. |
| `ollama.IsPromptOverflow(err)` | Un prompt no cabía en el contexto. |
| `ollama.IsTooLarge(err)` | La petición pasaba de 64 KiB. |
| `ollama.IsNotFound(err)` | El modelo no existe. |
| `ollama.IsUnavailable(err)` | No se pudo conectar con el servidor. |

## Límites del servidor

Verificados contra `POST /v1/systemone`:

| Límite | Valor | Si te pasas |
|---|---|---|
| Cuerpo de la petición | 64 KiB | `HTTP 413 request body must not exceed 64 KiB` |
| Prompt por pregunta | 1–8194 tokens | `HTTP 400 prompt 0 has 9144 tokens; expected 1–8194` |
| Preguntas por llamada | 1–64 | `HTTP 400 questions must contain 1–64 fields` |

**El prompt se construye una vez por pregunta, y cada uno lleva el estado
completo.** Por eso un estado largo se paga N veces: lo que limita el número de
preguntas no es el límite de 64, sino el presupuesto de contexto. La entrada
**nunca se recorta**; si no cabe, el servidor rechaza.

El cliente comprueba los 64 KiB en local y ofrece una estimación del contexto:

```go
if tokens := req.EstimatePromptTokens(); tokens > ollama.NimbleContextTokens {
    log.Printf("el prompt se estima en ~%d tokens", tokens)
}
```

`EstimatePromptTokens` es una heurística de ~4 caracteres por token, no un
tokenizador, y **se pasa por encima**: en una prueba con estado en español
estimaba 11.634 tokens donde el servidor contaba 9.238. Úsala para detectar un
estado desproporcionado y mira `usage.input_tokens` para el número real.

## Cómo interpretar los resultados

Bespoke Labs evaluó Nimble sobre 3.880 decisiones con etiquetas humanas en 13
conjuntos públicos (media macro):

| Tipo de pregunta | Precisión |
|---|---|
| `choice` | 81,6 % |
| `noul` | 80,2 % |
| `score` | 54,6 % |

Nimble 9B queda en 75,7 %, frente al 76,0 % de Jev 1.13 sobre la API de
TypeSafe. Consecuencias prácticas:

- **`score` es bastante más débil**, y en algunas rúbricas baja del 40 %. Úsalo
  para ordinalidad aproximada; prefiere `choice` cuando necesites precisión.
- **Las preguntas se califican de forma independiente.** El modelo no razona
  entre ellas, así que dos respuestas que deberían concordar pueden no hacerlo.
  Si necesitas consistencia, compruébala en tu código.
- **Una probabilidad de 0,9 no significa que la respuesta sea correcta el 90 %
  de las veces en tus datos**: es una confianza relativa dentro de esa pregunta.
  Por eso `--umbral` existe, aunque su 0,5 por defecto es solo un punto de
  partida.
- **`confidence` no es lo mismo.** Va de 0 a 1 y mide cuánto están concentradas
  las probabilidades. Un `confidence` bajo con la opción ganadora clara es un
  resultado sano, no un problema.
- **Añade una opción de «no coincide»** si es posible que la respuesta no sea
  ninguna de las que das. Si no, el modelo elige la más cercana sin avisarte.

### El nombre de la pregunta no es inocuo

La clave de cada pregunta entra en el prompt, así que **renombrarla cambia el
resultado**. Mesmo estado, mismas instrucciones, mismos criteria, solo la clave:

| Nombre | Probabilidad |
|---|---|
| `is_greeting` | 0,444 |
| `greetings` | 0,266 |
| `q` | 0,242 |
| `saludo` | 0,161 |
| `saludar` | 0,136 |

Un recorrido de 0,31 sobre una decisión que quizá compares con 0,5. Nombra las
preguntas de forma descriptiva y nunca con `q1` o `x`.

### Es determinista

Cinco llamadas seguidas al mismo estado dan el mismo valor bit a bit. No hace
falta sembrar para obtener reproducibilidad, y `--temperatura` y `--seed` están
disponibles igualmente.

## Rendimiento

Las preguntas en lote se amortizan solas: el coste crece con el número de
preguntas, pero mucho menos que proporcionalmente.

| Preguntas | Total | Tokens de entrada | Por pregunta |
|---|---|---|---|
| 1 | 453 ms | 167 | 453 ms |
| 4 | 1.607 ms | 1.304 | 402 ms |
| 16 | 6.324 ms | 15.590 | 395 ms |

A partir de unas 4 preguntas el coste **por pregunta** ya se ha aplanado. Ese es
el argumento para preguntar muchas cosas de una vez en lugar de repetir el
estado en llamadas sueltas.

`/v1/systemone` no tiene paso de razonamiento: emite **un único token de salida
por pregunta**. Le mandes lo que le mandes, no razona:

| Campo enviado | Resultado |
|---|---|
| (ninguno) | `noul: 0,9974`, salida 1 token |
| `"reasoning_effort": "high"` | `noul: 0,9974`, salida 1 token |
| `"think": true` | `noul: 0,9974`, salida 1 token |
| `"think": false` | `noul: 0,9974`, salida 1 token |

## Desarrollo

```bash
go test ./...            # unitarios, con httptest, sin red
go test -race -cover ./...
go vet ./...
gofmt -l .               # debe salir vacío
```

Los tests de integración se saltan salvo que actives la variable de entorno, de
modo que la CI no necesita ningún servidor:

```bash
SYSTEMONE_E2E=1 go test ./ollama -run Integration -v
```

Se pueden ajustar con `SYSTEMONE_URL` y `SYSTEMONE_MODEL`.

Antes de abrir un pull request, mira [CONTRIBUTING.md](CONTRIBUTING.md).

## Licencia

[MIT](LICENSE) © 2026 Aratan.

El modelo [Nimble](https://huggingface.co/Bespoke-Nimble-9B) es obra de
[Bespoke Labs](https://www.bespokelabs.ai) bajo licencia Apache 2.0. Este
repositorio no está afiliado a ellos ni a Ollama.
