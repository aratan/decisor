// Command decisor is a Spanish-language front end for the decision endpoint of
// an Ollama server.
//
// Usage:
//
//	decisor modelos
//	decisor preguntar --model M --estado "texto" --preguntas preguntas.json
//
// Every flag defaults to a local server; override it with --url.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/aratan/decisor/ollama"
)

const defaultModel = "nimble"

// warningBudget es el umbral a partir del cual la CLI avisa por el contexto.
// Supera el presupuesto real con holgura porque la estimación de tokens es
// aproximada y deliberadamente conservadora: avisar de más es preferible a
// perder una llamada, pero un aviso por poco margen sería ruido.
const warningBudget = ollama.NimbleContextTokens * 3 / 2

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("falta el comando")
	}

	// Ctrl-C cancels the in-flight request instead of leaving the model loaded.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	command, args := args[0], args[1:]
	var err error
	switch command {
	case "modelos", "models":
		err = runModels(ctx, args)
	case "preguntar", "decide":
		err = runDecide(ctx, args)
	case "-h", "--help", "ayuda", "help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("comando desconocido %q", command)
	}

	// Pedir la ayuda con -h es una petición correcta, no un fallo: el paquete
	// flag devuelve ErrHelp y hay que traduciro a salida limpia.
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return err
}

func usage() {
	fmt.Fprintf(os.Stderr, `decisor — cliente de Ollama para decisiones

Uso:
  decisor modelos                          lista los modelos disponibles
  decisor preguntar --estado "texto" --preguntas preguntas.json

Opciones comunes:
  --url      dirección del servidor (por defecto %s)
  --model    modelo a usar (por defecto %s)

Salida:
  --json     escribe la respuesta estructurada en JSON por stdout
  --salida   guarda además esa respuesta en JSON en el fichero indicado

Archivo de preguntas:
  Formato JSON idéntico al campo "questions" de la API, entre %d y %d entradas.
`, ollama.DefaultBaseURL, defaultModel, ollama.MinQuestions, ollama.MaxQuestions)
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "uso: decisor %s [opciones]\n\n", name)
		fs.PrintDefaults()
	}
	return fs
}

// -----------------------------------------------------------------------------
// modelos

func runModels(ctx context.Context, args []string) error {
	fs := newFlagSet("modelos")
	url := fs.String("url", ollama.DefaultBaseURL, "dirección del servidor")
	if err := fs.Parse(args); err != nil {
		return err
	}

	models, err := ollama.New(ollama.WithBaseURL(*url)).ListModels(ctx)
	if err != nil {
		return err
	}
	if len(models) == 0 {
		fmt.Println("El servidor no tiene modelos. Descarga uno con: ollama pull <modelo>")
		return nil
	}
	for _, model := range models {
		fmt.Println(model.ID)
	}
	return nil
}

// -----------------------------------------------------------------------------
// preguntar

func runDecide(ctx context.Context, args []string) error {
	fs := newFlagSet("preguntar")
	url := fs.String("url", ollama.DefaultBaseURL, "dirección del servidor")
	model := fs.String("model", defaultModel, "modelo a usar")
	state := fs.String("estado", "", "texto a evaluar")
	stateFile := fs.String("estado-archivo", "", "archivo JSON a evaluar como estado estructurado")
	questionsPath := fs.String("preguntas", "", "archivo JSON con las preguntas")
	threshold := fs.Float64("umbral", 0.5, "probabilidad mínima para responder 'sí'")
	systemPrompt := fs.String("sistema", "", "instrucciones de sistema para el clasificador")
	keepAlive := fs.String("mantener", "", "cuánto mantener el modelo cargado, p. ej. 5m")
	asJSON := fs.Bool("json", false, "imprime la respuesta estructurada en JSON por stdout")
	output := fs.String("salida", "", "guarda además la respuesta en JSON en este fichero")
	temperature := fs.Float64("temperatura", 0, "temperatura de muestreo; 0 usa el valor del servidor")
	seed := fs.Int("seed", 0, "semilla de muestreo; 0 usa la del servidor")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if strings.TrimSpace(*state) == "" && strings.TrimSpace(*stateFile) == "" {
		return errors.New("falta --estado o --estado-archivo")
	}
	if strings.TrimSpace(*questionsPath) == "" {
		return errors.New("falta --preguntas")
	}

	// El estado puede ser texto plano o un documento JSON estructurado.
	input, summary, err := buildState(*state, *stateFile)
	if err != nil {
		return err
	}

	questions, err := loadQuestions(*questionsPath)
	if err != nil {
		return err
	}

	request := ollama.DecideRequest{
		Model:     *model,
		State:     input,
		Questions: questions,
		System:    *systemPrompt,
		KeepAlive: *keepAlive,
	}
	if *temperature != 0 || *seed != 0 {
		request.Options = &ollama.Options{Temperature: floatPtr(*temperature), Seed: intPtr(*seed)}
	}

	// El estado se repite en el prompt de cada pregunta, así que conviene
	// avisar antes de gastar la llamada si no cabe en el contexto. La
	// estimación es aproximada y se pasa por encima, así que solo avisamos
	// cuando el margen es holgado: el servidor sigue siendo la autoridad.
	if estimate := request.EstimatePromptTokens(); estimate > warningBudget {
		fmt.Fprintf(os.Stderr,
			"advertencia: el prompt de cada pregunta se estima en ~%d tokens, por encima del presupuesto de %d de Nimble.\n"+
				"           La estimación se pasa; acorta el estado o las instrucciones si el servidor lo rechaza.\n",
			estimate, ollama.NimbleContextTokens)
	}

	client := ollama.New(ollama.WithBaseURL(*url))
	resp, err := client.Decide(ctx, request)
	if err != nil {
		return describeDecideError(err)
	}

	// El JSON va siempre por stdout con --json, y además a un fichero con
	// --salida. Los tiempos los mide el cliente, así que se añaden aparte:
	// la forma del servidor queda intacta.
	if *asJSON || *output != "" {
		encoded, err := encodeDecideJSON(resp)
		if err != nil {
			return err
		}
		if *asJSON {
			if _, err := os.Stdout.Write(encoded); err != nil {
				return err
			}
		}
		if *output != "" {
			if err := os.WriteFile(*output, append(encoded, '\n'), 0o644); err != nil {
				return fmt.Errorf("no se pudo escribir %s: %w", *output, err)
			}
			if !*asJSON {
				fmt.Printf("Respuesta guardada en %s\n", *output)
			}
		}
		if *asJSON {
			return nil
		}
	}
	printVerdicts(resp, summary, *threshold)
	return nil
}

// decideJSON is the DecideResponse in the server's shape, plus the timings the
// client measured. Duration lives outside the API response, so it is surfaced
// here rather than in the library types.
type decideJSON struct {
	Model   string                   `json:"model"`
	Answers map[string]ollama.Answer `json:"answers"`
	Usage   ollama.Usage             `json:"usage"`
	Timing  timingJSON               `json:"timing"`
	Order   []string                 `json:"order,omitempty"`
}

type timingJSON struct {
	// DurationMS is the whole round trip, as measured by the client.
	DurationMS float64 `json:"duration_ms"`
	// PerQuestionMS amortises the round trip across the questions asked,
	// which is the number to watch when batching many of them: the cost of a
	// call grows with the questions, but far less than proportionally.
	PerQuestionMS float64 `json:"per_question_ms"`
}

func encodeDecideJSON(resp *ollama.DecideResponse) ([]byte, error) {
	payload := decideJSON{
		Model:   resp.Model,
		Answers: resp.Answers,
		Usage:   resp.Usage,
		Order:   resp.Order,
		Timing: timingJSON{
			DurationMS: milliseconds(resp.Duration),
		},
	}
	if n := len(resp.Answers); n > 0 {
		payload.Timing.PerQuestionMS = milliseconds(resp.Duration) / float64(n)
	}

	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("no se pudo codificar la respuesta: %w", err)
	}
	return encoded, nil
}

func milliseconds(d time.Duration) float64 {
	return math.Round(float64(d.Microseconds())/10) / 100
}

// formatDuration renders a duration with a sensible unit for humans.
func formatDuration(d time.Duration) string {
	switch {
	case d >= time.Minute:
		return fmt.Sprintf("%.2f min", d.Minutes())
	case d >= time.Second:
		return fmt.Sprintf("%.2f s", d.Seconds())
	default:
		return fmt.Sprintf("%.0f ms", float64(d.Microseconds())/1000)
	}
}

// describeDecideError traduce los fallos que más se dan a un consejo concreto
// en lugar de repetir el mensaje del servidor.
func describeDecideError(err error) error {
	switch {
	case ollama.IsPromptOverflow(err):
		return fmt.Errorf("%w\n"+
			"  El estado se repite en el prompt de cada pregunta: acórtalo o reparte las preguntas\n"+
			"  en varias llamadas. Nimble no recorta la entrada, la rechaza.", err)
	case ollama.IsTooLarge(err):
		return fmt.Errorf("%w\n  La petición supera los 64 KiB: acorta el estado o las instrucciones", err)
	case ollama.IsNotFound(err):
		return fmt.Errorf("%w\n  Comprueba el nombre con: decisor modelos", err)
	default:
		return err
	}
}

// buildState resuelve el estado a evaluar: texto plano de --estado, o el
// documento JSON de --estado-archivo. Devuelve también un resumen para imprimir.
func buildState(text, path string) (ollama.State, string, error) {
	if strings.TrimSpace(text) == "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, "", fmt.Errorf("no se pudo leer %s: %w", path, err)
		}
		if !json.Valid(raw) {
			return nil, "", fmt.Errorf("%s no contiene JSON válido", path)
		}
		return ollama.JSONState(raw), fmt.Sprintf("%s (JSON, %d bytes)", path, len(raw)), nil
	}
	return ollama.StringState(text), text, nil
}

// loadQuestions reads the questions file, whose contents are the value of the
// API's "questions" field. Names are validated with the same rules the library
// applies in Go.
func loadQuestions(path string) (map[string]ollama.Question, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer %s: %w", path, err)
	}

	var rawQuestions map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawQuestions); err != nil {
		return nil, fmt.Errorf("%s no contiene un objeto de preguntas: %w", path, err)
	}
	if len(rawQuestions) < ollama.MinQuestions || len(rawQuestions) > ollama.MaxQuestions {
		return nil, fmt.Errorf("%s define %d preguntas; se admiten entre %d y %d",
			path, len(rawQuestions), ollama.MinQuestions, ollama.MaxQuestions)
	}

	questions := make(map[string]ollama.Question, len(rawQuestions))
	for name, rawQuestion := range rawQuestions {
		question, err := ollama.ParseQuestion(rawQuestion)
		if err != nil {
			return nil, fmt.Errorf("pregunta %q: %w", name, err)
		}
		questions[name] = question
	}
	return questions, nil
}

// printVerdicts renders the answers in the order the server returned them,
// which is the order the questions were sent in.
func printVerdicts(resp *ollama.DecideResponse, state string, threshold float64) {
	ordered := resp.AnswersInOrder()

	fmt.Printf("Modelo:  %s\n", resp.Model)
	fmt.Printf("Entrada: %s\n\n", truncate(state, 160))

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PREGUNTA\tTIPO\tRESPUESTA\tCONFIANZA")
	for _, named := range ordered {
		fmt.Fprintf(w, "%s\t%s\t%s\t%.2f\n",
			named.Name, named.Answer.Type, result(named.Answer, threshold), answerConfidence(named.Answer))
	}
	// Nothing useful to show if the writer failed; stdout is going away anyway.
	_ = w.Flush()

	fmt.Printf("\nTokens: %d de entrada, %d de salida", resp.Usage.InputTokens, resp.Usage.OutputTokens)
	fmt.Printf("   Duración: %s", formatDuration(resp.Duration))
	if n := len(resp.Answers); n > 1 {
		fmt.Printf(" (%s por pregunta)", formatDuration(resp.Duration/time.Duration(n)))
	}
	fmt.Println()

	if detail := probabilityDetail(ordered); detail != "" {
		fmt.Printf("\nProbabilidades:\n%s\n", detail)
	}
}

// result renders the human-facing outcome of one answer.
func result(answer ollama.Answer, threshold float64) string {
	switch answer.Type {
	case ollama.QuestionNoul:
		noul, _ := answer.AsNoul()
		if noul.Verdict(threshold) {
			return "sí"
		}
		return "no"
	case ollama.QuestionChoice:
		choice, _ := answer.AsChoice()
		return choice.Selected
	case ollama.QuestionScore:
		score, _ := answer.AsScore()
		index, label, ok := score.Bucket()
		if ok {
			return fmt.Sprintf("%s (%d)", label, index)
		}
		return fmt.Sprintf("%.2f", score.Value)
	default:
		return "?"
	}
}

// answerConfidence reports the confidence for choice and score answers; for
// noul the probability already carries the confidence.
func answerConfidence(answer ollama.Answer) float64 {
	switch answer.Type {
	case ollama.QuestionNoul:
		return answer.Noul
	default:
		return answer.Confidence
	}
}

// probabilityDetail lists per-option probabilities, which is where the nuance
// of a borderline decision shows up.
func probabilityDetail(ordered []ollama.NamedAnswer) string {
	var b strings.Builder
	for _, named := range ordered {
		answer := named.Answer
		if len(answer.Probabilities) == 0 {
			continue
		}

		keys := make([]string, 0, len(answer.Probabilities))
		for key := range answer.Probabilities {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			a, errA := strconv.Atoi(keys[i])
			c, errC := strconv.Atoi(keys[j])
			if errA == nil && errC == nil {
				return a < c
			}
			return keys[i] < keys[j]
		})

		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			label := key
			if legend, ok := answer.Legend[key]; ok && legend != "" {
				label = fmt.Sprintf("%s=%s", key, legend)
			}
			parts = append(parts, fmt.Sprintf("%s %.2f", label, answer.Probabilities[key]))
		}
		fmt.Fprintf(&b, "  %s: %s\n", named.Name, strings.Join(parts, " · "))
	}
	return strings.TrimRight(b.String(), "\n")
}

// -----------------------------------------------------------------------------
// utilidades

func floatPtr(v float64) *float64 {
	if v == 0 {
		return nil
	}
	return &v
}

func intPtr(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}

func truncate(s string, limit int) string {
	runes := []rune(strings.Join(strings.Fields(s), " "))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "…"
}
