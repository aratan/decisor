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

## Comprobar que funciona

Sin navegador ni Ollama, y sin más herramienta que `node`:

```bash
./demo/probar.sh
```

Valida la sintaxis del JavaScript, que la demo no arrastre dependencias y que
una partida entera funcione: carga `batalla.js` sobre un DOM mínimo, levanta un
servidor simulado que imita `POST /v1/systemone` y juega hasta hundir el barco.
De paso comprueba lo que la partida no debería romper nunca —`choice` con una
criteria por casilla libre y entre 2 y 26 opciones— y que el mapa de calor, las
barras de probabilidad y el contador de impactos se pinten. Una línea por
comprobación y código de salida distinto de cero si algo falla.

Eso no sustituye a la prueba a mano con el modelo real: para eso, `./demo/run.sh`.

## Ficha técnica

- Vanilla HTML+JS, sin dependencias ni build.
- Estado mínimo (`mapa`, `impactos`, `disparos`) dentro de los límites de la
  API (64 KiB, 8194 tokens/pregunta, 1–64 preguntas).
- El demo vive en `demo/`, fuera de `examples/`, para que el test raíz
  `TestEjemplosDelRepositorio` no lo valide.
- `demo/probar.sh` y `demo/probar.js` lo prueban sin navegador; el arnés usa
  solo módulos estándar de Node, así que la regla de cero dependencias sigue
  en pie en todo el repositorio.
