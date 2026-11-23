package fab

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func brainRepo(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	write := func(rel, body string) {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(d, rel)), 0o755)
		if err := os.WriteFile(filepath.Join(d, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("lecciones.md", "# Lecciones\n")
	write("modulos/ventas/ventas.md", "# Ventas\n")
	write("REGLAS.md", "# Reglas\n")
	// Un link roto viejo no es de la fábrica: el lint mira solo lo agregado.
	write("modulos/viejo.md", "# Viejo\nver [[ya-no-existe]]\n")
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "base"}} {
		if out, err := exec.Command("git", append([]string{"-C", d}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return d
}

func TestBrainLint(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		rel    string
		body   string
		quiero string // "" = sin problemas
	}{
		{"lección con link que existe", "lecciones.md", "# Lecciones\n- regla, ver [[ventas]] (factory, tarea x)\n", ""},
		{"lección sin la marca", "lecciones.md", "# Lecciones\n- regla (tarea x)\n", "sin la marca factory"},
		{"link con alias escapado en tabla", "lecciones.md", "# Lecciones\n- | [[ventas\\|Ventas]] | (factory) |\n", ""},
		{"lección general que nombra un proyecto", "lecciones.md", "# Lecciones\n- en Acme el saldo (factory)\n", "nombra al proyecto acme"},
		{"nota de proyecto con su alcance", "modulos/stock/acme.md", "---\ntipo: modulo\nfuente: tarea x\netiquetas: [factory]\nalcance: proyecto\n---\n# Stock de acme\n", ""},
		{"fuera de lo permitido", "REGLAS.md", "# Reglas\ncambié algo\n", "fuera de lo permitido"},
		{"nota nueva sin frontmatter", "modulos/stock/stock.md", "# Stock\n", "sin frontmatter"},
		{"nota nueva con frontmatter", "modulos/ventas/cobros.md", "---\ntipo: modulo\nfuente: tarea x\netiquetas: [factory]\n---\n# Cobros\n", ""},
		{"wikilink roto", "modulos/ventas/ventas.md", "# Ventas\nver [[no-existe|alias]]\n", "wikilink roto"},
		{"lo viejo roto no frena", "modulos/viejo.md", "# Viejo\nver [[ya-no-existe]]\nagrego [[ventas]]\n", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := brainRepo(t)
			_ = os.MkdirAll(filepath.Dir(filepath.Join(d, c.rel)), 0o755)
			_ = os.WriteFile(filepath.Join(d, c.rel), []byte(c.body), 0o644)
			probs, err := brainLint(ctx, d, []string{"lecciones.md", "modulos/"}, []string{"acme"})
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Join(probs, "; ")
			if (c.quiero == "") != (got == "") || !strings.Contains(got, c.quiero) {
				t.Fatalf("problemas = %q, quería %q", got, c.quiero)
			}
		})
	}
}

func TestCommitBrainDescartaSiElLintFalla(t *testing.T) {
	ctx := context.Background()
	d := brainRepo(t)
	_ = os.WriteFile(filepath.Join(d, "REGLAS.md"), []byte("# Reglas\nmetido\n"), 0o644)
	_ = os.WriteFile(filepath.Join(d, "nueva.md"), []byte("x\n"), 0o644)
	if _, err := commitBrain(ctx, d, "x", []string{"lecciones.md", "modulos/"}, nil); err == nil {
		t.Fatal("tenía que fallar")
	}
	st, _ := exec.Command("git", "-C", d, "status", "--porcelain").Output()
	if len(st) != 0 {
		t.Fatalf("tenía que quedar limpio: %s", st)
	}
}

// Una cita corta aparece en cualquier lado: no sostiene una respuesta.
func TestCitaCortaNoVale(t *testing.T) {
	d := brainRepo(t)
	larga := Cita{Archivo: "lecciones.md", Linea: 1, Texto: "# Lecciones"}
	if citaExiste(d, larga) {
		t.Fatal("11 caracteres no alcanzan")
	}
	if err := os.WriteFile(filepath.Join(d, "REGLAS.md"), []byte("# Reglas\nlos importes se guardan en centavos enteros\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !citaExiste(d, Cita{Archivo: "REGLAS.md", Linea: 2, Texto: "importes se guardan en centavos"}) {
		t.Fatal("una cita larga que existe vale")
	}
}

// Las contradicciones del lint van al tablero, una tarjeta por día.
func TestTarjetaDeLint(t *testing.T) {
	c := Config{Runs: t.TempDir()}
	r := Respuesta{Sabe: true, Respuesta: "dos reglas chocan", Citas: []Cita{{Archivo: "a.md", Linea: 1, Texto: "regla uno larga"}}}
	c.tarjetaDeLint(r)
	c.tarjetaDeLint(r)
	ts, _ := LoadTasks(c.Runs)
	if len(ts) != 1 || ts[0].Est.Estado != Propuesta || !strings.Contains(ts[0].Est.Titulo, "dos reglas chocan") {
		t.Fatalf("%+v", ts)
	}
	if err := Aceptar(c.Runs, ts[0].Est.ID); err == nil {
		t.Fatal("un aviso del cerebro no va a la línea")
	}
}
