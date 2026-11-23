package fab

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyCitas(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "conocimiento"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "conocimiento", "nota.md"), []byte("uno\ndos\nLos centavos van en int64.\ncuatro\n"), 0o644)
	_ = os.WriteFile(filepath.Join(filepath.Dir(root), "afuera.md"), []byte("secreto\n"), 0o644)

	cases := []struct {
		name string
		cita Cita
		ok   bool
	}{
		{"línea exacta", Cita{"conocimiento/nota.md", 3, "centavos van en int64"}, true},
		{"dentro de la ventana", Cita{"conocimiento/nota.md", 1, "centavos van en int64"}, true},
		{"texto inventado", Cita{"conocimiento/nota.md", 3, "los centavos van en float"}, false},
		{"archivo inexistente", Cita{"conocimiento/otra.md", 1, "uno"}, false},
		{"línea fuera del archivo", Cita{"conocimiento/nota.md", 40, "uno"}, false},
		{"no sale de la carpeta", Cita{"../afuera.md", 1, "secreto"}, false},
		{"texto vacío", Cita{"conocimiento/nota.md", 1, " "}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := citaExiste(root, c.cita); got != c.ok {
				t.Fatalf("citaExiste = %v, quería %v", got, c.ok)
			}
		})
	}

	r := VerifyCitas(root, Respuesta{Sabe: true, Citas: []Cita{{"conocimiento/nota.md", 3, "inventado"}}})
	if r.Sabe || r.Aviso == "" || len(r.Descartadas) != 1 {
		t.Fatalf("sin citas verificadas tiene que bajar a no sabe: %+v", r)
	}
}
