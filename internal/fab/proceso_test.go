package fab

import (
	"bytes"
	"context"
	"os/exec"
	"testing"
	"time"
)

// Un hijo que ignora la señal y se queda con la salida abierta no cuelga la espera.
func TestEnGrupoMataALosHijos(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", "trap '' TERM; sleep 60 & sleep 60")
	var out bytes.Buffer
	cmd.Stdout = &out
	enGrupo(cmd)
	inicio := time.Now()
	_ = cmd.Run()
	if d := time.Since(inicio); d > esperaHijos+5*time.Second {
		t.Fatalf("tardó %v: el grupo no se mató", d)
	}
}
