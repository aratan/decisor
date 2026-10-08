# Demo: batalla naval 5×5

Demo visual de decisor: el modelo `nimble` elige casilla por casilla contra
`POST /v1/systemone` de Ollama y la página pinta un **mapa de calor de
probabilidades** sobre el tablero — el mensaje que interesa: probabilidades,
no prosa.

## Cómo ejecutarla

Atajo: `./demo/run.sh` hace los cuatro pasos por ti (comprueba Ollama y el
modelo, levanta el servidor estático con el origen CORS correcto y abre el
navegador en la página). Los pasos manuales son estos:

1. Arranca Ollama con un modelo Nimble o Tev (los únicos que acepta
   `/v1/systemone`):

   ```bash
   ollama pull nimble
   ```

2. Permite el CORS del navegador (necesario porque la página llama a Ollama
   directamente):

   ```bash
   OLLAMA_ORIGINS=http://localhost:8000 ollama serve
   ```

3. Sirve la demo desde localhost (abrir el HTML con `file://` no funciona):

   ```bash
   python3 -m http.server        # en la raíz del repo
   ```

4. Abre <http://localhost:8000/demo/batalla.html>.

## Qué hace

- **Dispara el modelo**: cada turno envía el estado del tablero y pide una
  `choice` sobre las casillas sin disparar; dibuja las `probabilities`
  devueltas como intensidad de color y resalta la casilla elegida.
- **Yo disparo**: modo manual para comparar tu puntería con la del modelo.
- **Nueva partida**: reposiciona el barco de 3 casillas.
- Si queda una sola casilla sin disparar, la dispara sin llamar al modelo
  (`choice` exige entre 2 y 26 criterios).
- Si Ollama no responde, muestra un error claro en español; no hace falta
  nada más.

## Ficha técnica

- Vanilla HTML+JS, sin dependencias ni build.
- Estado mínimo (`mapa`, `impactos`, `disparos`) dentro de los límites de la
  API (64 KiB, 8194 tokens/pregunta, 1–64 preguntas).
- El demo vive en `demo/`, fuera de `examples/`, para que el test raíz
  `TestEjemplosDelRepositorio` no lo valide.
