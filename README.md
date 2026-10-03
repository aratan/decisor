# decisor

Cliente Go sin dependencias externas para un servidor Ollama. Cubre dos APIs:

- **`POST /v1/systemone`** — el endpoint de decisiones: evalúa un texto contra
  un conjunto de preguntas tipadas y devuelve *probabilidades* en lugar de prosa.
- **`POST /v1/chat/completions`** — la API de chat compatible con OpenAI, que
  también funciona contra proveedores remotos que hablan el mismo dialecto.

Incluye una biblioteca reutilizable (`ollama/`) y una CLI en español
(`cmd/decisor`) con dos comandos: `modelos` y `preguntar`.

## Instalación

```bash
go build ./cmd/decisor      # genera ./decisor
go test ./...                # tests unitarios, sin red
```

Sin dependencias externas: solo la biblioteca estándar.

## El endpoint `systemone`

El esquema lo descubrí sondeando el servidor, y tiene un par de sorpresas que
conviene documentar:

1. **Los campos van en inglés**, aunque un ejemplo circulaba traducido:
   `modelo` → `model`, `Estado` → `state`, `Preguntas` → `questions`,
   `instrucciones` → `instructions`, `criterios` → `criteria`. Enviar los
   nombres en español devuelve `{"error":"model is required"}`.
2. **`noul` es el nombre real del tipo booleano**, con el typo incluido. Los
   tipos válidos son exactamente `noul`, `choice` y `score`.

La petición cruda equivalente al ejemplo es:

```json
{
  "model": "nimble",
  "state": "Hola Mundo",
  "questions": {
    "says_hello": {
      "type": "noul",
      "instructions": "Does the state text contain a greeting?",
      "criteria": {
        "true": "The state text contains a greeting.",
        "false": "The state text does not contain a greeting"
      }
    }
  }
}
```

Y la respuesta:

```json
{
  "model": "nimble",
  "answers": {
    "says_hello": { "type": "noul", "noul": 0.945 }
  },
  "usage": { "input_tokens": 162, "output_tokens": 1 }
}
```

### Los tres tipos de pregunta

| Tipo | `criteria` | Qué devuelve |
|---|---|---|
| `noul` | **opcional**: `{"true": "...", "false": "..."}` para describir cada lado | `noul`: probabilidad de que sea cierto |
| `choice` | obligatorio: objeto `{"clave": "descripción"}`, de 2 a 26 opciones | `choice` + `probabilities` + `confidence` |
| `score` | obligatorio: serie de descripciones de nivel, la más baja primero, numerados desde 0 | `score` (el nivel ponderado por probabilidad) + `legend` + `probabilities` + `confidence` |

Dos detalles que conviene conocer, porque no son evidentes y la documentación
del modelo no los refleja del todo:

- **Omitir `criteria` no es lo mismo que enviarlo como `null`.** En una
  pregunta `noul`, omitirlo es válido y deja la decisión en las instrucciones,
  pero un `null` explícito se rechaza con
  `noul criteria must be an object of true/false descriptions`. El cliente lo
  resuelve siempre por omisión.
- La documentación sugiere que `criteria: null` permite que `choice` se
  describa a sí mismo, pero en la práctica **este build lo rechaza** con
  `criteria must contain 2–26 candidates`. `choice` y `score` necesitan
  `criteria` de verdad.

`confidence` va de 0 a 1 y mide **cuánto están concentradas las probabilidades,
no la probabilidad de que la respuesta sea correcta**. Un `confidence` bajo con
la opción ganadora clara es un resultado sano.

Otros límites: **entre 1 y 64 preguntas** por llamada. Los campos opcionales de
nivel superior son `options`, `system` y `keep_alive`.

### Límites verificados

Tres restricciones que no se deducen del ejemplo y que conviene conocer antes de
construir encima:

| Límite | Valor | Qué pasa si lo pasas |
|---|---|---|
| Cuerpo de la petición | 64 KiB | HTTP 413 `request body must not exceed 64 KiB` |
| Prompt por pregunta | 1–8194 tokens | HTTP 400 `prompt 0 has 9144 tokens; expected 1–8194` |
| Preguntas por llamada | 1–64 | HTTP 400 `questions must contain 1–64 fields` |

**El prompt se construye una vez por pregunta, y cada uno lleva el estado
completo.** Por eso un estado largo se paga N veces y el número de preguntas que
cabe depende de lo largo que sea el estado, no al revés. La entrada **nunca se
recorta**: si no cabe, el servidor rechaza.

El cliente comprueba el límite de 64 KiB en local y expone una estimación del
presupuesto:

```go
if tokens := req.EstimatePromptTokens(); tokens > ollama.NimbleContextTokens {
    log.Printf("el prompt se estima en ~%d tokens", tokens)
}
```

`EstimatePromptTokens` es una heurística de ~4 caracteres por token, no un
tokenizador, y se pasa por encima de la cuenta real: en una prueba con estado en
español estimaba 11.634 tokens donde el servidor contaba 9.238. Úsala para
detectar un estado desproporcionado, y fíjate en `usage.input_tokens` de la
respuesta para el número real. La CLI solo avisa cuando el margen es holgado.

Los tres casos de error se distinguen con `IsPromptOverflow`, `IsTooLarge` y
`IsNotFound`.

### Cómo interpretar los resultados

Bespoke Labs evaluó Nimble sobre 3.880 decisiones con etiquetas humanas en 13
conjuntos de datos públicos (media macro):

| Tipo de pregunta | Precisión |
|---|---|
| `choice` | 81,6 % |
| `noul` (booleano) | 80,2 % |
| `score` (rúbrica) | 54,6 % |

En su conjunto Nimble 9B queda en 75,7 %, frente al 76,0 % de Jev 1.13 sobre la
API de TypeSafe. Consecuencias prácticas:

- **`score` es bastante más débil** que los otros dos tipos, y en algunas
  rúbricas baja del 40 %. Úsalo para ordinalidad aproximada, no como medida
  fiable; prefiere `choice` cuando necesites precisión.
- **Las preguntas se califican de forma independiente.** El modelo no razona
  entre ellas, así que dos respuestas que deberían concordar pueden no hacerlo.
  Si necesitas consistencia, compruébala en tu código.
- **Una probabilidad de 0,9 no significa que la respuesta sea correcta el 90 %
  de las veces en tus datos**: es una confianza relativa dentro de esa pregunta.
  Por eso `--umbral` existe, aunque su valor por defecto (0,5) es solo un punto
  de partida: calibra con tus propios datos antes de confiar en él.
- **Añade una opción de «no coincide»** si es posible que la respuesta no sea
  ninguna de las que das. Si no, el modelo elige la más cercana sin avisarte.
- **Solo texto.** Nimble no anida JSON, no escribe explicaciones ni devuelve
  citas. El enrutado de varias decisiones encadenadas hazlo en tu código.
- **Instrucciones cortas.** Cada pregunta paga el coste del estado completo, y
  los prompts más cortos son los mejor probados por Bespoke Labs.

### El nombre de la pregunta no es inocuo

La clave de cada pregunta entra en el prompt, así que **renombrarla cambia el
resultado**. Medido con el mismo estado, las mismas instrucciones y los mismos
criterios, solo cambiando la clave:

| Nombre de la pregunta | Probabilidad |
|---|---|
| `is_greeting` | 0,444 |
| `greetings` | 0,266 |
| `q` | 0,242 |
| `x` | 0,211 |
| `saludo` | 0,161 |
| `saludar` | 0,136 |

Un recorrido de 0,31 sobre una decisión que quizá compares con 0,5. Nombre las
preguntas de forma descriptiva y nunca con `q1`, `x` o similar: el modelo lee
esa clave.

### Es determinista

Nimble no introduce ruido entre ejecuciones: cinco llamadas seguidas al mismo
estado dan el mismo valor bit a bit (0,3789383791273048), y fijar
`temperatura` y `seed` no cambia nada. No hace falta sembrar para obtener
reproducibilidad, y `--temperatura` / `--seed` están disponibles igualmente.

### El estado puede ser estructurado

`state` acepta una cadena, un objeto JSON o un array:

```go
resp, err := client.Decide(ctx, ollama.DecideRequest{
    Model: "nimble",
    State: map[string]any{"autor": "Ana", "mensaje": "Hola, ¿cómo estás?"},
    Questions: questions,
})
```

### Las respuestas vuelven en orden

El servidor devuelve las respuestas **en el mismo orden en que se enviaron las
preguntas**. Como `Answers` es un mapa (que en Go no tiene orden), el cliente
guarda la secuencia en `DecideResponse.Order` y la reproduce con
`AnswersInOrder()`.

Nimble no tiene paso de razonamiento: lee el mensaje una vez por pregunta y
 puntúa directamente los tokens de respuesta. Por eso las respuestas tardan
menos de 100 ms en hardware de escritorio y conviene poner hasta 64 preguntas
en una sola llamada en vez de repetir el texto.

## Uso de la biblioteca

### Decisiones

```go
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

// Los criterios son opcionales: un valor cero deja que el modelo decida
// solo a partir de las instrucciones, y la clave "criteria" se omite.
containsGreeting, err := ollama.NewNoulQuestion("¿El texto contiene un saludo?", ollama.NoulCriteria{})

tone, err := ollama.NewChoiceQuestion(
    "¿Cuál es el tono del texto?",
    ollama.ChoiceCriteria{
        "saludo":    "El texto es un saludo.",
        "despedida": "El texto es una despedida.",
        "otro":      "El texto no es ni uno ni otro.",
    },
)

client := ollama.New() // http://localhost:11434 por defecto

resp, err := client.Decide(ctx, ollama.DecideRequest{
    Model:     "nimble",
    State:     "Hola, ¿cómo estás? Me llamo Ana.",
    Questions: map[string]ollama.Question{"saludo": greeting, "tono": tone},
})
if err != nil {
    return err
}

// Las respuestas son una unión discriminada por `type`: usa el accesor
// correcto para que los tipos coincidan.
if noul, ok := resp.Answers["saludo"].AsNoul(); ok && noul.Verdict(0.5) {
    fmt.Println("es un saludo con probabilidad", noul.Probability)
}

if choice, ok := resp.Answers["tono"].AsChoice(); ok {
    top, probability := choice.Top()
    fmt.Printf("tono=%s (%.2f)\n", top, probability)
}

// Las respuestas vienen en el orden en que se enviaron las preguntas.
for _, named := range resp.AnswersInOrder() {
    fmt.Println(named.Name, named.Answer.Type)
}
```

Las preguntas se validan **en local** antes de salir a la red, así que los
errores de esquema vienen como `ErrInvalidRequest` y no como un HTTP 400:

```go
if _, err := ollama.NewChoiceQuestion("Elige", ollama.ChoiceCriteria{"solo": "Única"}); err != nil {
    // ErrInvalidRequest: se requieren entre 2 y 26 opciones
}
```

Para cargar preguntas desde un archivo o configuración, `ParseQuestion` valida
un `json.RawMessage` con el mismo formato que usa la API.

### Chat (solo biblioteca)

El cliente también habla con `/v1/chat/completions`, el endpoint compatible con
OpenAI, por si quieres apuntarlo a un proveedor remoto. **No es lo que Nimble
hace bien**: como modelo de decisión responde con códigos de una letra. Úsalo con
modelos de chat.

```go
resp, err := client.Chat(ctx, ollama.ChatRequest{
    Model:    "otro-modelo",
    Messages: []ollama.Message{ollama.Text("Explícame Go en dos frases")},
})
fmt.Println(resp.Text())
fmt.Println(resp.Duration) // lo mide el cliente
```

Con streaming por server-sent events y sus tiempos:

```go
stats, err := client.ChatStreamWithStats(ctx, req, func(chunk ollama.ChatChunk) error {
    fmt.Print(chunk.Text())       // contenido visible
    fmt.Print(chunk.Reasoning())  // razonamiento, si el modelo lo expone
    return nil
})
// stats.Duration      total, desde que sale la petición
// stats.FirstToken    hasta el primer texto: en un modelo que razona, aquí
//                     se ve el coste del razonamiento
// stats.TokensPerSecond
```

### Configuración

```go
client := ollama.New(
    ollama.WithBaseURL("https://api.example.com/v1"), // por defecto localhost:11434
    ollama.WithAPIKey("..."),                        // cabecera Authorization
    ollama.WithHeader("X-Trace", "abc"),
    ollama.WithTimeout(30*time.Second),               // no usar con streaming
)
```

`WithBaseURL` y `WithAPIKey` existen para que el mismo código funcione contra
proveedores remotos compatibles con OpenAI.

## CLI

```bash
./decisor modelos
./decisor preguntar --model nimble \
    --estado "Hola, ¿cómo estás? Me llamo Ana." \
    --preguntas examples/preguntas.json
```

Salida de `preguntar`:

```
Modelo:  nimble
Entrada: Hola, ¿cómo estás? Me llamo Ana. Adiós.

PREGUNTA       TIPO    RESPUESTA    CONFIANZA
is_question    noul    no           0.01
politeness     score   educado (1)  0.25
says_greeting  noul    sí           0.98
tone           choice  greeting     0.34

Tokens: 1541 de entrada, 5 de salida   Duración: 1.52 s (380 ms por pregunta)

Probabilidades:
  politeness: 0=descortés 0.07 · 1=educado 0.65 · 2=muy educado 0.28
  tone: farewell 0.27 · greeting 0.69 · other 0.03
```

El archivo de preguntas es el valor del campo `questions` de la API; ver
[`examples/preguntas.json`](examples/preguntas.json). Otras opciones:
`--umbral` (probabilidad para responder «sí»), `--sistema`, `--mantener`,
`--url`, `--temperatura`, `--seed` y `--estado-archivo` (estado estructurado
en lugar de texto plano).

### Salida estructurada

`--json` escribe la respuesta en JSON por stdout, y `--salida` la guarda además
en el fichero indicado. Se pueden combinar: con ambos, el JSON va a stdout y al
fichero, y no se imprime el resumen legible.

```bash
./decisor preguntar ... --json | jq '.answers.tone.probabilities'
./decisor preguntar ... --salida decision.json
```

```json
{
  "model": "nimble",
  "answers": {
    "tone": { "type": "choice", "choice": "greeting",
              "probabilities": { "greeting": 0.93, "farewell": 0.05, "other": 0.02 },
              "confidence": 0.73 }
  },
  "usage": { "input_tokens": 1553, "output_tokens": 5 },
  "timing": { "duration_ms": 1789.5, "per_question_ms": 447.375 },
  "order": ["is_question", "politeness", "says_greeting", "tone"]
}
```

`answers` mantiene la forma exacta de la API. `timing` y `order` los añade la
CLI: los tiempos los mide el cliente, no el servidor.

### Tiempos

La duración se mide en el cliente y no altera la forma del JSON de la API (los
tipos de la biblioteca la guardan en `Duration`, con `json:"-"`).

Las preguntas en lote se amortizan solas: el coste crece con el número de
preguntas, pero mucho menos que proporcionalmente, porque el estado se procesa
una vez.

| Preguntas | Total | Tokens de entrada | Por pregunta |
|---|---|---|---|
| 1 | 453 ms | 167 | 453 ms |
| 4 | 1.607 ms | 1.304 | 402 ms |
| 16 | 6.324 ms | 15.590 | 395 ms |

A partir de unas 4 preguntas el coste **por pregunta** ya se ha aplanado. Ese es
el argumento para preguntar muchas cosas de una vez en lugar de repetir el
estado en llamadas sueltas.

### Razonamiento

`preguntar` llama a `/v1/systemone`, que emite **un único token de salida por
pregunta** y no razona: no hay nada que ajustar, y por eso la CLI no expone
`-esfuerzo` ni ninguna otra perilla. El endpoint de decisiones **ignora en
silencio** cualquier mando de razonamiento:

| Campo enviado a `/v1/systemone` | Resultado |
|---|---|
| (ninguno) | `noul: 0,9974`, salida 1 token |
| `"reasoning_effort": "high"` | `noul: 0,9974`, salida 1 token |
| `"think": true` | `noul: 0,9974`, salida 1 token |
| `"think": false` | `noul: 0,9974`, salida 1 token |

Por lo mismo la CLI no tiene subcomando de chat: nimble está ajustado para
elegir una etiqueta, y en el endpoint de chat responde con códigos de una letra
(`"C"`, `"A"`).

### Nota sobre Nimble

Nimble es un **modelo de decisión**: responde preguntas sobre un texto, no
conversa. Por eso `decisor` no tiene subcomando de chat. Aunque el endpoint de
chat lo acepta y lo trata como un modelo `thinking`, responde con **códigos de
una letra** (`"B"`, `"k"`, `"A"`) en lugar de prosa, porque está ajustado para
emitir la etiqueta de una opción. Para conversación real usa un modelo de chat.
Licencia Apache 2.0.

## Tests

Los tests unitarios usan `httptest` y no tocan la red:

```bash
go test ./...
```

Los tests de integración se saltan salvo que actives la variable de entorno:

```bash
SYSTEMONE_E2E=1 go test ./ollama -run Integration -v
```

Se pueden ajustar con `SYSTEMONE_URL` y `SYSTEMONE_MODEL` (por defecto
`localhost:11434` y `nimble`).

## Estructura

```
ollama/
  client.go          cliente base, opciones y transporte
  errors.go          *APIError, ErrInvalidRequest, ErrUnavailable
  systemone.go       tipos, validación y respuestas de /v1/systemone
  chat.go            chat compatible con OpenAI y streaming SSE
  models.go          /v1/models y /api/version
cmd/decisor/        CLI en español
examples/            archivo de preguntas de ejemplo
```
