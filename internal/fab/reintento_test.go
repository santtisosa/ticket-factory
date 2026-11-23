package fab

import (
	"context"
	"errors"
	"testing"
)

// Una traba de infraestructura vuelve sola a la cola una vez; la segunda se queda trabada.
func TestTrabarInfraReintentaUnaVez(t *testing.T) {
	runs := t.TempDir()
	tk, err := AddTask(runs, "x", "prompt", EnCurso, "demo")
	if err != nil {
		t.Fatal(err)
	}
	c := Config{Runs: runs}
	c.trabarInfra(context.Background(), tk, "spec: se cayó")
	if tk.Est.Estado != Pendiente {
		t.Fatalf("la primera vuelve a la cola: %s", tk.Est.Estado)
	}
	tk.Est.Estado = EnCurso
	if err := SetEstado(runs, tk.Est.ID, Pendiente, EnCurso, ""); err != nil {
		t.Fatal(err)
	}
	c.trabarInfra(context.Background(), tk, "spec: se cayó de nuevo")
	if tk.Est.Estado != Trabada {
		t.Fatalf("la segunda se traba: %s", tk.Est.Estado)
	}
	if !errors.Is(errInfra{errors.New("timeout")}, ErrInfra) || errors.Is(ErrSinCuota, ErrInfra) {
		t.Fatal("ErrInfra marca las caídas y no la cuota")
	}
}
