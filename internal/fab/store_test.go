package fab

import (
	"os"
	"path/filepath"
	"testing"
)

// Un evento tardío (el Bibliotecario después de la aprobación) no pisa el estado: solo Transicion lo cambia.
func TestLogNoPisaElEstado(t *testing.T) {
	runs := t.TempDir()
	tk, err := AddTask(runs, "x", "prompt", EsperandoAprobacion, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := SetEstado(runs, tk.Est.ID, EsperandoAprobacion, PRAbierto, "aprobar"); err != nil {
		t.Fatal(err)
	}
	tk.Log("06-bibliotecario", "ok", "nada nuevo", 0.1)
	ts, _ := LoadTasks(runs)
	if ts[0].Est.Estado != PRAbierto {
		t.Fatalf("el evento tardío pisó el estado: %s", ts[0].Est.Estado)
	}
	if err := tk.Transicion(EsperandoAprobacion, Trabada, "x"); err == nil {
		t.Fatal("una transición desde un estado viejo tiene que fallar")
	}
}

// Lo que quedó EN_CURSO al cortarse la fábrica vuelve a la cola al arrancar.
func TestRecuperarEnCurso(t *testing.T) {
	runs := t.TempDir()
	a, _ := AddTask(runs, "a", "prompt", Pendiente, "")
	if err := a.Transicion(Pendiente, EnCurso, ""); err != nil {
		t.Fatal(err)
	}
	n, err := RecuperarEnCurso(runs)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	ts, _ := LoadTasks(runs)
	if ts[0].Est.Estado != Pendiente {
		t.Fatalf("quería PENDIENTE: %s", ts[0].Est.Estado)
	}
}

// La primera vez se importa lo que había en archivos, aunque un estado.json esté cortado.
func TestImportaLoDeAntes(t *testing.T) {
	runs := t.TempDir()
	ok := filepath.Join(runs, "20261025-1926-ee7b")
	roto := filepath.Join(runs, "20261025-1948-e1cd")
	_ = os.MkdirAll(ok, 0o755)
	_ = os.MkdirAll(roto, 0o755)
	_ = os.WriteFile(filepath.Join(ok, "estado.json"), []byte(`{"id":"20261025-1926-ee7b","titulo":"t","estado":"ESPERANDO_APROBACION","costo_usd":1.5}`), 0o644)
	_ = os.WriteFile(filepath.Join(ok, "events.jsonl"), []byte(`{"ts":"2026-10-25T19:48:10-03:00","paso":"02-spec","estado":"ok","mensaje":"m","costo_usd":0.2}`+"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(roto, "estado.json"), []byte(`{"id":"2026`), 0o644)
	ts, err := LoadTasks(runs)
	if err != nil || len(ts) != 1 || ts[0].Est.Estado != EsperandoAprobacion {
		t.Fatalf("importación: %v %v", ts, err)
	}
	if evs := ts[0].LoadEvents(); len(evs) != 1 || evs[0].Paso != "02-spec" {
		t.Fatalf("eventos importados: %v", evs)
	}
}
