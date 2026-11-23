package fab

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"unicode"
)

// Lo único que publica (la aprobación y cerebro-sync, desde la página o la terminal) lo confirma una
// persona por un canal que no es la petición que lo pidió. Un Builder llega a localhost y puede
// falsificar el Origin y la cabecera X-Fabrica con curl: por eso el botón de la página solo pide, y la
// confirmación ocurre en el diálogo del sistema o en la terminal de control del proceso de la fábrica,
// que arrancó una persona. Los agentes y el código de la rama corren en su propia sesión (Setsid,
// proceso.go), así que no heredan esa terminal. Que un agente no ejecute `fabrica aprobar` por su
// cuenta (con una terminal propia) lo impiden la lista deny y el sandbox, sin red a GitHub ni
// credenciales, no este archivo.

// esperaConfirmacion: sin respuesta en este tiempo, no se confirma.
const esperaConfirmacion = 2 * time.Minute

// Confirmador pregunta a una persona. clave es lo que se escribe para confirmar en la terminal.
type Confirmador interface {
	Confirmar(ctx context.Context, texto, clave string) (bool, error)
}

var (
	errSinConfirmar = errors.New("no se confirmó: no se publicó nada")
	errSinCanal     = errors.New("no hay un canal de confirmación seguro; ejecute `fabrica aprobar` desde una terminal")
)

// elegirConfirmador devuelve el canal disponible, en orden: el diálogo nativo de macOS, zenity en un
// escritorio Linux, la terminal de control. Se elige uno solo: su "no", su silencio o su error son
// finales (variable para los tests).
var elegirConfirmador = func() (Confirmador, error) {
	if runtime.GOOS == "darwin" {
		if _, err := os.Stat(osascript); err == nil {
			return dialogoMac{}, nil
		}
	}
	if runtime.GOOS == "linux" && (os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != "") {
		if z, err := exec.LookPath("zenity"); err == nil {
			return dialogoZenity{ruta: z}, nil
		}
	}
	if tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
		tty.Close()
		return terminal{abrir: func() (io.ReadWriteCloser, error) { return os.OpenFile("/dev/tty", os.O_RDWR, 0) }}, nil
	}
	return nil, errSinCanal
}

// confirmar pregunta por el canal disponible. nil solo si la persona confirmó.
func confirmar(ctx context.Context, texto, clave string) error {
	cf, err := elegirConfirmador()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, esperaConfirmacion)
	defer cancel()
	ok, err := cf.Confirmar(ctx, sinControl(texto), clave)
	switch {
	case err != nil:
		return fmt.Errorf("la confirmación falló (%v): no se publicó nada", err)
	case !ok || ctx.Err() != nil:
		return errSinConfirmar
	}
	return nil
}

// topeTitulo: cuántas runas del título de la tarea entran en el diálogo.
const topeTitulo = 120

// unaLinea deja el título (viene del ticket) en una sola línea visible: sin saltos (tampoco U+2028 ni
// U+2029), sin caracteres de control ni de formato (Cf: marcas de dirección, ancho cero), con los
// espacios colapsados y como mucho topeTitulo runas. Así no puede empujar el encabezado fuera de la
// vista ni imitar otra línea del diálogo.
func unaLinea(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t' || r == '\u2028' || r == '\u2029':
			return ' '
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if rs := []rune(s); len(rs) > topeTitulo {
		s = string(rs[:topeTitulo-1]) + "…"
	}
	return s
}

// textoPublicar arma el texto del diálogo: primero un encabezado fijo con lo que se publica, después
// el título en una línea y, si el pedido llegó por la página, el aviso de que vino de ahí.
func textoPublicar(repos []string, titulo string, desdePanel bool, detalle string) string {
	texto := "Publicar en GitHub: " + strings.Join(repos, ", ") + "\n\n" + unaLinea(titulo)
	if desdePanel {
		texto += "\n\nPedido desde el panel web."
	}
	if detalle != "" {
		texto += "\n\n" + detalle
	}
	return texto
}

// sinControl saca los caracteres de control del texto (viene del ticket): una secuencia de escape no
// puede reescribir lo que se ve en la terminal.
func sinControl(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, s)
}

const osascript = "/usr/bin/osascript"

// dialogoMac: el diálogo nativo. El texto va como argumento, nunca dentro del script. Pide escribir la
// clave (el id de la tarea): un proceso con permiso de Accesibilidad podría pulsar el botón con System
// Events, pero tendría que adivinar además qué escribir. El script devuelve lo escrito solo si se pulsó
// Aprobar; "No" sale con error y el vencimiento devuelve vacío.
type dialogoMac struct{}

func (dialogoMac) Confirmar(ctx context.Context, texto, clave string) (bool, error) {
	out, _ := exec.CommandContext(ctx, osascript,
		"-e", "on run argv",
		"-e", "activate",
		"-e", `set r to display dialog (item 1 of argv) default answer "" with title "ticket-factory" buttons {"No", "Aprobar"} default button "No" cancel button "No" giving up after 120`,
		"-e", `if gave up of r then return ""`,
		"-e", `if button returned of r is "Aprobar" then return text returned of r`,
		"-e", `return ""`,
		"-e", "end run",
		textoConClave(texto, clave)).Output()
	return claveCoincide(string(out), clave), nil
}

func textoConClave(texto, clave string) string {
	return texto + "\n\nPara confirmar escriba " + clave + " y pulse Aprobar."
}

// claveCoincide: lo que devolvió el diálogo es exactamente la clave (sin espacios alrededor).
func claveCoincide(out, clave string) bool { return clave != "" && strings.TrimSpace(out) == clave }

// dialogoZenity: escritorio Linux. Sale 0 solo con "Aprobar"; cancelar, cerrar o el timeout dan otro código.
// ponytail: en X11 cualquier cliente del display puede sintetizar el click (los agentes no reciben
// DISPLAY, pero podrían adivinarlo); en Wayland no.
type dialogoZenity struct{ ruta string }

func (z dialogoZenity) Confirmar(ctx context.Context, texto, _ string) (bool, error) {
	err := exec.CommandContext(ctx, z.ruta, "--question", "--title=ticket-factory", "--no-markup",
		"--text="+texto, "--ok-label=Aprobar", "--cancel-label=No", "--default-cancel", "--timeout=120").Run()
	return err == nil, nil
}

// terminal: pide escribir la clave en la terminal de control (/dev/tty, nunca stdin).
// ponytail: si se vence el tiempo, la lectura queda colgada hasta el próximo Enter; solo se descarta.
type terminal struct {
	abrir func() (io.ReadWriteCloser, error)
}

func (t terminal) Confirmar(ctx context.Context, texto, clave string) (bool, error) {
	tty, err := t.abrir()
	if err != nil {
		return false, err
	}
	fmt.Fprintf(tty, "\n%s\n\nPara confirmar escriba %q y pulse Enter (cualquier otra cosa cancela): ", texto, clave)
	linea := make(chan string, 1)
	go func() {
		s, _ := bufio.NewReader(tty).ReadString('\n')
		linea <- s
	}()
	select {
	case s := <-linea:
		tty.Close()
		return strings.TrimSpace(s) == clave, nil
	case <-ctx.Done():
		fmt.Fprintln(tty, "\nsin respuesta: no se confirma")
		tty.Close()
		return false, nil
	}
}
