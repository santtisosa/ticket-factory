package fab

import (
	"os/exec"
	"syscall"
	"time"
)

// esperaHijos: después de matar el grupo, cuánto se espera a que suelten la salida antes de cortar.
const esperaHijos = 10 * time.Second

// enGrupo hace que cancelar el contexto (timeout, Ctrl-C, cancelar) mate al proceso y a todos sus
// hijos: el npx de Playwright, el `go test` o el `next dev` que lanzó un experto. Sin esto, un
// hijo que se queda con la salida abierta colgaba la espera más allá del timeout. Setsid (no solo
// Setpgid): una sesión nueva sin terminal de control, así ni el agente ni el código que el chequeo
// ejecuta pueden abrir /dev/tty para responder la confirmación de la aprobación (confirmar.go). El
// grupo sigue siendo -pid.
func enGrupo(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = esperaHijos
}

// Corte de un experto: SIGINT cierra el turno y deja que `claude -p` imprima su resultado (con el
// costo); si no sale, SIGTERM y al final SIGKILL a todo el grupo. Con SIGKILL directo la salida
// quedaba vacía ("signal: killed") y el trabajo se perdía.
const (
	graciaInterrupt = 20 * time.Second
	graciaTerm      = 5 * time.Second
)

func enGrupoSuave(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		pgid := -cmd.Process.Pid
		_ = syscall.Kill(pgid, syscall.SIGINT)
		// ponytail: chequeo de vida antes de cada señal; un pgid reciclado en 25 s es improbable.
		pid := cmd.Process.Pid
		go func() {
			// Si el principal ya salió (cerró el turno), los hijos que ignoran SIGINT (un proceso de fondo
			// de un Bash) no pueden seguir reteniendo la salida: se los corta enseguida.
			for limite := time.Now().Add(graciaInterrupt); time.Now().Before(limite); time.Sleep(200 * time.Millisecond) {
				if syscall.Kill(pid, 0) != nil {
					_ = syscall.Kill(pgid, syscall.SIGKILL)
					return
				}
			}
			if syscall.Kill(pgid, 0) != nil {
				return
			}
			_ = syscall.Kill(pgid, syscall.SIGTERM)
			time.Sleep(graciaTerm)
			if syscall.Kill(pgid, 0) == nil {
				_ = syscall.Kill(pgid, syscall.SIGKILL)
			}
		}()
		return nil
	}
	cmd.WaitDelay = graciaInterrupt + graciaTerm + esperaHijos
}

// matarGrupo: SIGKILL al grupo del proceso ya terminado, por lo que haya dejado de fondo (un servidor
// de un test, un proceso que esperaba a la sesión). Se llama siempre después de Wait, aunque el ctx no
// se haya cancelado. Un proceso que se hizo su propia sesión (setsid) se escapa: queda como riesgo
// documentado (docs/seguridad.md).
// ponytail: el número de grupo podría reciclarse entre el Wait y la señal; improbable en milisegundos.
func matarGrupo(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
