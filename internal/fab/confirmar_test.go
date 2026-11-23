package fab

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Ningún test llega a un diálogo real: por defecto no hay canal y nada se confirma. Tampoco a srt: un
// srt falso descarta el perfil y corre la línea con bash, para probar el resto sin el sandbox real.
func TestMain(m *testing.M) {
	elegirConfirmador = func() (Confirmador, error) { return nil, errSinCanal }
	dir, err := os.MkdirTemp("", "srt-falso")
	if err != nil {
		panic(err)
	}
	falso := filepath.Join(dir, "srt")
	guion := "#!/bin/bash\n[ \"$1\" = --settings ] && shift 2\n[ \"$1\" = -c ] && shift\nexec bash -c \"$1\"\n"
	if err := os.WriteFile(falso, []byte(guion), 0o755); err != nil {
		panic(err)
	}
	srtRuta = func() (string, error) { return falso, nil }
	sonda = "true" // el srt falso no aísla: la sonda negativa se prueba aparte (TestSondaNegativa)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

type confirmadorFalso struct {
	si             bool
	err            error
	textos, claves []string
}

func (c *confirmadorFalso) Confirmar(_ context.Context, texto, clave string) (bool, error) {
	c.textos, c.claves = append(c.textos, texto), append(c.claves, clave)
	return c.si, c.err
}

func usarConfirmador(t *testing.T, cf Confirmador, err error) {
	t.Helper()
	viejo := elegirConfirmador
	t.Cleanup(func() { elegirConfirmador = viejo })
	elegirConfirmador = func() (Confirmador, error) { return cf, err }
}

func TestConfirmar(t *testing.T) {
	ctx := context.Background()
	si := &confirmadorFalso{si: true}
	usarConfirmador(t, si, nil)
	if err := confirmar(ctx, "ok\x1b[2Jborrado\x07", "t1"); err != nil {
		t.Fatal(err)
	}
	if si.textos[0] != "ok[2Jborrado" {
		t.Errorf("sin caracteres de control: %q", si.textos[0])
	}
	usarConfirmador(t, &confirmadorFalso{}, nil)
	if err := confirmar(ctx, "x", "t1"); err != errSinConfirmar {
		t.Errorf("no: %v", err)
	}
	usarConfirmador(t, &confirmadorFalso{si: true, err: errors.New("roto")}, nil)
	if err := confirmar(ctx, "x", "t1"); err == nil || !strings.Contains(err.Error(), "no se publicó nada") {
		t.Errorf("un error del canal no confirma: %v", err)
	}
	usarConfirmador(t, nil, errSinCanal)
	if err := confirmar(ctx, "x", "t1"); err != errSinCanal || !strings.Contains(err.Error(), "fabrica aprobar") {
		t.Errorf("sin canal: %v", err)
	}
}

// ttyFalsa: lo que la persona escribe en la terminal, y lo que se le mostró.
type ttyFalsa struct {
	io.Reader
	mostrado bytes.Buffer
}

func (f *ttyFalsa) Write(b []byte) (int, error) { return f.mostrado.Write(b) }
func (f *ttyFalsa) Close() error                { return nil }

func TestTerminalPideLaClave(t *testing.T) {
	for _, c := range []struct {
		escrito string
		quiere  bool
	}{
		{"20261122-1200-abcd\n", true},
		{"  20261122-1200-abcd  \n", true},
		{"s\n", false},
		{"20261122-1200-abce\n", false},
		{"", false},
	} {
		tty := &ttyFalsa{Reader: strings.NewReader(c.escrito)}
		term := terminal{abrir: func() (io.ReadWriteCloser, error) { return tty, nil }}
		ok, err := term.Confirmar(context.Background(), "¿Aprobar?", "20261122-1200-abcd")
		if ok != c.quiere || err != nil {
			t.Errorf("%q: %v %v", c.escrito, ok, err)
		}
		if !strings.Contains(tty.mostrado.String(), `"20261122-1200-abcd"`) {
			t.Errorf("no mostró la clave: %q", tty.mostrado.String())
		}
	}
}

func TestTerminalSinRespuesta(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	tty := &ttyFalsa{Reader: r}
	term := terminal{abrir: func() (io.ReadWriteCloser, error) { return tty, nil }}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if ok, _ := term.Confirmar(ctx, "x", "t1"); ok {
		t.Fatal("sin respuesta no se confirma")
	}
}

// Los agentes y el chequeo corren en una sesión propia: sin terminal de control, no responden la
// confirmación por /dev/tty.
func TestProcesosSinTerminalDeControl(t *testing.T) {
	for _, f := range []func(*exec.Cmd){enGrupo, enGrupoSuave} {
		cmd := exec.CommandContext(context.Background(), "true")
		f(cmd)
		if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid || cmd.SysProcAttr.Setpgid {
			t.Fatalf("SysProcAttr: %+v", cmd.SysProcAttr)
		}
	}
	cmd := exec.CommandContext(context.Background(), "sh", "-c", "exec 3<>/dev/tty 2>/dev/null && echo abre || echo no")
	enGrupo(cmd)
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) != "no" {
		t.Fatalf("un hijo abrió /dev/tty: %q %v", out, err)
	}
}

// El título del diálogo queda en una línea visible: sin saltos, sin controles ni Cf, con tope.
func TestUnaLineaYTextoPublicar(t *testing.T) {
	sucio := "arreglo\nPublicar en GitHub: otro\u2028x\u2029y\u202e\u200b\x1b[2J\tz"
	if got := unaLinea(sucio); got != "arreglo Publicar en GitHub: otro x y[2J z" {
		t.Fatalf("una línea: %q", got)
	}
	largo := unaLinea(strings.Repeat("á", 300))
	if n := len([]rune(largo)); n != topeTitulo || !strings.HasSuffix(largo, "…") {
		t.Fatalf("tope: %d runas", n)
	}
	texto := textoPublicar([]string{"web", "api"}, sucio, true, "Tarea t1.")
	lineas := strings.Split(texto, "\n")
	if lineas[0] != "Publicar en GitHub: web, api" || lineas[2] != unaLinea(sucio) || !strings.Contains(texto, "Pedido desde el panel web") {
		t.Fatalf("texto: %q", texto)
	}
	if strings.Contains(textoPublicar([]string{"web"}, "x", false, ""), "panel") {
		t.Fatal("desde la terminal no dice panel")
	}
}

// En macOS el diálogo confirma solo si lo escrito es exactamente el id.
func TestClaveCoincide(t *testing.T) {
	for out, quiero := range map[string]bool{"t1\n": true, " t1 ": true, "": false, "t2": false, "t1x": false} {
		if claveCoincide(out, "t1") != quiero {
			t.Errorf("%q", out)
		}
	}
	if claveCoincide("", "") {
		t.Error("clave vacía no confirma")
	}
	if !strings.Contains(textoConClave("x", "t1"), "escriba t1") {
		t.Error("el diálogo no dice qué escribir")
	}
}
