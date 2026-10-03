// Package decisor valida los ficheros de ejemplos del repositorio.
//
// Los JSON de examples/ se escriben a mano, y es fácil que uno se quede mal
// formado o trague un carácter corrupto sin que se note al leerlo. Este test
// los recorre en cada `go test ./...`, así que un ejemplo roto no llega a
// publicarse.
//
// Se apoya en ParseQuestions para el esquema: son las mismas reglas que aplica
// la biblioteca, no una copia.
package decisor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"unicode"

	"github.com/aratan/decisor/ollama"
)

// noEsPreguntas identifica ficheros que hay en examples/ pero que no son un
// conjunto de preguntas.
var noEsPreguntas = map[string]bool{
	"estado.json": true,
}

// comprobarEjemplo devuelve los problemas de un fichero de preguntas. Separado
// del test para poder verificarlo con casos malos, que es la única forma
// saber que el detector sirve de algo.
func comprobarEjemplo(ruta string) ([]string, error) {
	crudo, err := os.ReadFile(ruta)
	if err != nil {
		return nil, err
	}

	var problemas []string

	// Caracteres de alfabetos que no deberían aparecer en un ejemplo en
	// español. Es la señal de que el texto se corrompió al redactarlo.
	if linea, caracter := caracterRaro(string(crudo)); caracter != "" {
		problemas = append(problemas, fmt.Sprintf("línea %d: carácter fuera del alfabeto latino: %q", linea, caracter))
	}

	// Reglas del endpoint, mediante el validador de la biblioteca.
	if _, err := ollama.ParseQuestions(crudo); err != nil {
		problemas = append(problemas, err.Error())
	}

	// Higiene del fichero: claves sospechosas.
	var claves map[string]json.RawMessage
	if err := json.Unmarshal(crudo, &claves); err == nil {
		for _, clave := range sortedKeys(claves) {
			if clave == "" {
				problemas = append(problemas, "hay una clave vacía")
			}
			if strings.HasPrefix(clave, "_") {
				problemas = append(problemas, fmt.Sprintf("la clave %q empieza por guion bajo", clave))
			}
		}
	}

	return problemas, nil
}

// caracterRaro devuelve la línea y el carácter si el texto contiene letra de un
// alfabero que no debería aparecer aquí.
func caracterRaro(texto string) (int, string) {
	alfabetos := []*unicode.RangeTable{
		unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul,
		unicode.Cyrillic, unicode.Arabic, unicode.Hebrew, unicode.Devanagari,
		unicode.Thai, unicode.Armenian, unicode.Georgian,
	}
	for numero, linea := range strings.Split(texto, "\n") {
		for _, caracter := range linea {
			for _, alfabeto := range alfabetos {
				if unicode.Is(alfabeto, caracter) {
					return numero + 1, string(caracter)
				}
			}
		}
	}
	return 0, ""
}

func sortedKeys(m map[string]json.RawMessage) []string {
	claves := make([]string, 0, len(m))
	for clave := range m {
		claves = append(claves, clave)
	}
	sort.Strings(claves)
	return claves
}

func TestEjemplosDelRepositorio(t *testing.T) {
	t.Parallel()

	ruta, err := filepath.Abs("examples")
	if err != nil {
		t.Fatalf("localizando examples/: %v", err)
	}

	entradas, err := os.ReadDir(ruta)
	if err != nil {
		t.Fatalf("leyendo examples/: %v", err)
	}

	encontrados := 0
	for _, entrada := range entradas {
		nombre := entrada.Name()
		if entrada.IsDir() || !strings.HasSuffix(nombre, ".json") || noEsPreguntas[nombre] {
			continue
		}
		encontrados++

		t.Run(nombre, func(t *testing.T) {
			problemas, err := comprobarEjemplo(filepath.Join(ruta, nombre))
			if err != nil {
				t.Fatalf("no se pudo leer: %v", err)
			}
			for _, problema := range problemas {
				t.Errorf("%s: %s", nombre, problema)
			}
		})
	}

	if encontrados == 0 {
		t.Fatal("no se encontró ningún fichero de preguntas en examples/")
	}
}

// Un detector que nunca falla no sirve de nada, así que se comprueba con casos
// que sí están rotos a propósito.
//
// Los mensajes que se buscan son los de la biblioteca, que están en inglés
// porque el código va en inglés; la CLI es la que los traduce.
func TestComprobarEjemploDetectaProblemas(t *testing.T) {
	t.Parallel()

	tests := []struct {
		nombre    string
		contenido string
		quiere    []string // subcadenas que deben aparecer en algún problema
	}{
		{
			nombre:    "tipo desconocido",
			contenido: `{"q": {"type": "oracle", "instructions": "?"}}`,
			quiere:    []string{"oracle"},
		},
		{
			nombre:    "una sola opción",
			contenido: `{"q": {"type": "choice", "instructions": "?", "criteria": {"solo": "Única"}}}`,
			quiere:    []string{"2", "26"},
		},
		{
			nombre:    "escala vacía",
			contenido: `{"q": {"type": "score", "instructions": "?", "criteria": []}}`,
			quiere:    []string{"at least one description"},
		},
		{
			nombre:    "sin instrucciones",
			contenido: `{"q": {"type": "noul", "instructions": "   "}}`,
			quiere:    []string{"non-empty instructions"},
		},
		{
			nombre:    "carácter CJK",
			contenido: `{"q": {"type": "noul", "instructions": "¿一 reference?"}}`,
			quiere:    []string{"alfabeto latino"},
		},
		{
			nombre:    "carácter cirílico",
			contenido: `{"q": {"type": "noul", "instructions": "Привет"}}`,
			quiere:    []string{"alfabeto latino"},
		},
		{
			nombre:    "clave con guion bajo",
			contenido: `{"_malo": {"type": "noul", "instructions": "ok"}}`,
			quiere:    []string{"guion bajo"},
		},
		{
			nombre:    "conjunto vacío",
			contenido: `{}`,
			quiere:    []string{"1", "64"},
		},
		{
			nombre:    "JSON mal formado",
			contenido: `{"q": {"type": "noul", "instructions": "sin cerrar"`,
			quiere:    []string{"invalid"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.nombre, func(t *testing.T) {
			t.Parallel()

			ruta := filepath.Join(t.TempDir(), "caso.json")
			if err := os.WriteFile(ruta, []byte(tt.contenido), 0o600); err != nil {
				t.Fatalf("escribiendo caso: %v", err)
			}

			problemas, err := comprobarEjemplo(ruta)
			if err != nil {
				t.Fatalf("no se pudo leer: %v", err)
			}
			if len(problemas) == 0 {
				t.Fatal("se esperaba algún problema y no se encontró ninguno")
			}

			junto := strings.Join(problemas, "\n")
			for _, want := range tt.quiere {
				if !strings.Contains(junto, want) {
					t.Errorf("esperaba %q en los problemas, obtuvo:\n%s", want, junto)
				}
			}
		})
	}
}

// Un ejemplo correcto no debe dar falsos positivos.
func TestComprobarEjemploAceptaUnFicheroBueno(t *testing.T) {
	t.Parallel()

	bueno := `{
		"saludo":  {"type": "noul", "instructions": "¿Contiene un saludo?"},
		"tono":    {"type": "choice", "instructions": "¿Qué tono?", "criteria": {"a": "A", "b": "B"}},
		"calidad": {"type": "score", "instructions": "¿Qué calidad?", "criteria": ["baja", "media", "alta"]}
	}`

	ruta := filepath.Join(t.TempDir(), "bueno.json")
	if err := os.WriteFile(ruta, []byte(bueno), 0o600); err != nil {
		t.Fatalf("escribiendo caso: %v", err)
	}

	problemas, err := comprobarEjemplo(ruta)
	if err != nil {
		t.Fatalf("no se pudo leer: %v", err)
	}
	if len(problemas) != 0 {
		t.Fatalf("un fichero válido dio problemas: %v", problemas)
	}
}
