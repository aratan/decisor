# Cómo contribuir

Gracias por mirar el proyecto. Toda ayuda vale, desde corregir una errata hasta
discutir el diseño de la API.

## Puesta en marcha

No hay dependencias externas: solo la biblioteca estándar de Go.

```bash
git clone https://github.com/aratan/decisor.git
cd decisor
go test ./...
go build -o decisor ./cmd/decisor
```

Para probar contra un Ollama de verdad necesitas un modelo con capacidad de
decisión instalado:

```bash
ollama pull nimble
./decisor preguntar --estado "Hola" --preguntas examples/preguntas.json
```

## Antes de abrir un pull request

```bash
gofmt -l .        # debe salir vacío
go vet ./...
go test -race ./...
```

Las tres coinciden con lo que ejecuta la integración continua, así que fallan
antes de llegar a GitHub.

`go test` incluye además un test que recorre `examples/` y valida cada fichero
con `ParseQuestions`. Si tocas un ejemplo, ese test te avisa; no hace falta
ninguna herramienta aparte.

## Cómo escribir código

- **Sin dependencias.** El paquete `ollama/` usa solo la biblioteca estándar.
  Si un PR introduce una dependencia, hay que justificarlo en el cuerpo.
- **Error antes que validación perezosa.** Los constructores de preguntas
  validan en local y devuelven errores que envuelven `ollama.ErrInvalidRequest`,
  para que el usuario no pague un viaje al servidor por un esquema malo.
- **Errores que se puedan traducir.** Interpreta el mensaje del servidor cuando
  sea un caso conocido (`IsPromptOverflow`, `IsTooLarge`, `IsNotFound`); si no,
  devuélvelo tal cual.
- **Nada de claves ni endpoints escritos a pelo.** Todo por `WithBaseURL`.
- **No rompas la forma del JSON.** `Answer` implementa `MarshalJSON` y
  `UnmarshalJSON` a propósito; si añades un campo, actualiza los dos y su test
  de ida y vuelta.

## Tests

- Los unitarios usan `httptest` y **no tocan la red**: `go test ./...`.
- Los de integración se saltan salvo que actives `SYSTEMONE_E2E=1`:

  ```bash
  SYSTEMONE_E2E=1 go test ./ollama -run Integration -v
  ```

  Se pueden ajustar con `SYSTEMONE_URL` y `SYSTEMONE_MODEL`.

Si cambias el comportamiento de `Decide`, añade el test que lo cubre. Si el
cambio depende de cómo se comporta el servidor, idealmente con un test de
integración.

## Reportar un fallo

Incluye la salida de `--json`, la versión del servidor
(`curl localhost:11434/api/version`), el modelo y la petición concreta que lo
reproduce. Con eso suele bastar para reproducirlo.

Si el fallo depende de la respuesta del modelo y parece improbable a primera
vista, la causa más común ya está documentada: **el nombre de la pregunta entra
en el prompt**, así que renombrarla cambia el resultado. Mismo texto, nombres
distintos, probabilidades distintas.

## Conducta

Sé claro y respetuoso. Si discutes el diseño, ataca la idea y no a la persona.

## Licencia

Al contribuir aceptas que tu trabajo se publique bajo la [licencia MIT](LICENSE).
