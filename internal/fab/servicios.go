package fab

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// esperaServicio: cuánto se espera a que el puerto de un servicio acepte conexiones.
const esperaServicio = 2 * time.Minute

// docker corre `docker` con su salida combinada (variable para los tests).
var docker = func(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "docker", args...).CombinedOutput()
}

// puertoListo espera a que 127.0.0.1:puerto acepte conexiones (variable para los tests).
var puertoListo = esperarPuerto

// asegurarServicios levanta (o reusa) los servicios del proyecto de la tarea y devuelve las
// variables de `exporta` con {puerto} ya reemplazado. Sin servicios declarados no llama a Docker.
func (c Config) asegurarServicios(ctx context.Context, repos []string) ([]string, error) {
	if len(repos) == 0 {
		return nil, nil
	}
	cs := Clientes(c.Home)
	_, proyecto, ok := buscarRepo(cs, repos[0])
	if !ok {
		return nil, nil
	}
	e, err := CargarEspacio(c.Home)
	if err != nil {
		return nil, err
	}
	var env []string
	for i, s := range e.Proyectos[proyecto].Servicios {
		nombre := fmt.Sprintf("fabrica-%s-%d", proyecto, i)
		puerto, err := levantarServicio(ctx, nombre, s)
		if err == nil {
			err = puertoListo(ctx, puerto, esperaServicio)
		}
		if err != nil {
			return nil, fmt.Errorf("%s (%s): %w%s", nombre, s.Imagen, err, estadoContenedor(ctx, nombre))
		}
		env = append(env, exportar(s.Exporta, puerto)...)
	}
	return env, nil
}

// huellaServicio identifica la configuración de un servicio: si cambia la imagen, el puerto o el
// entorno, el contenedor se recrea.
func huellaServicio(s Servicio) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%d", s.Imagen, s.Puerto)
	for _, k := range sortedKeys(s.Entorno) {
		fmt.Fprintf(h, "|%s=%s", k, s.Entorno[k])
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// levantarServicio deja el contenedor corriendo y devuelve el puerto de 127.0.0.1 que Docker le
// asignó. Si existe con otra configuración, se recrea.
func levantarServicio(ctx context.Context, nombre string, s Servicio) (int, error) {
	puertoCont := strconv.Itoa(s.Puerto) + "/tcp"
	huella := huellaServicio(s)
	out, err := docker(ctx, "inspect", "-f", `{{index .Config.Labels "fabrica.config"}}`, nombre)
	existe := err == nil
	if existe && strings.TrimSpace(string(out)) != huella {
		if out, err := docker(ctx, "rm", "-f", nombre); err != nil {
			return 0, fmt.Errorf("docker no pudo reemplazar %s: %s", nombre, tail(string(out)))
		}
		existe = false
	}
	if existe {
		if out, err := docker(ctx, "start", nombre); err != nil {
			return 0, fmt.Errorf("docker no arranca %s: %s", nombre, tail(string(out)))
		}
	} else {
		// 127.0.0.1::<puerto>: Docker elige un puerto libre de la máquina, sin carrera con otros procesos.
		args := []string{"run", "-d", "--name", nombre, "--label", "fabrica.config=" + huella, "-p", "127.0.0.1::" + strconv.Itoa(s.Puerto)}
		for _, k := range sortedKeys(s.Entorno) {
			args = append(args, "-e", k+"="+s.Entorno[k])
		}
		if out, err := docker(ctx, append(args, s.Imagen)...); err != nil {
			return 0, fmt.Errorf("docker no crea %s (%s): %s", nombre, s.Imagen, tail(string(out)))
		}
	}
	// El puerto se lee cada vez: cambia al reiniciar el contenedor.
	out, err = docker(ctx, "port", nombre, puertoCont)
	if err != nil {
		return 0, fmt.Errorf("docker no informa el puerto de %s: %s", nombre, tail(string(out)))
	}
	return puertoPublicado(string(out))
}

// estadoContenedor: el estado y el final del log del contenedor, para que el motivo diga por qué no
// levantó (una imagen que sale al arrancar porque le falta una variable, por ejemplo).
func estadoContenedor(ctx context.Context, nombre string) string {
	estado, err := docker(ctx, "inspect", "-f", "{{.State.Status}} (salida {{.State.ExitCode}})", nombre)
	if err != nil {
		return ""
	}
	logs, _ := docker(ctx, "logs", "--tail", "15", nombre)
	return "; contenedor " + strings.TrimSpace(string(estado)) + "; log: " + tail(string(logs))
}

// puertoPublicado lee la salida de `docker port` ("127.0.0.1:49153", una línea por dirección).
func puertoPublicado(out string) (int, error) {
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		l = strings.TrimSpace(l)
		if i := strings.LastIndex(l, ":"); i >= 0 {
			if p, err := strconv.Atoi(l[i+1:]); err == nil && p > 0 {
				return p, nil
			}
		}
	}
	return 0, fmt.Errorf("salida de docker port inesperada: %q", out)
}

// exportar arma las variables KEY=valor, en orden, con {puerto} reemplazado.
func exportar(vars map[string]string, puerto int) []string {
	env := make([]string, 0, len(vars))
	for _, k := range sortedKeys(vars) {
		env = append(env, k+"="+strings.ReplaceAll(vars[k], "{puerto}", strconv.Itoa(puerto)))
	}
	return env
}

// esperarPuerto: el puerto acepta una conexión y no la cierra enseguida. El proxy de Docker acepta
// aunque el servicio de adentro todavía no escuche, pero en ese caso corta la conexión al instante.
// ponytail: "escucha" no es "listo para consultas"; si una imagen lo necesita, sumar un comando de salud.
func esperarPuerto(ctx context.Context, puerto int, espera time.Duration) error {
	dir := net.JoinHostPort("127.0.0.1", strconv.Itoa(puerto))
	limite := time.Now().Add(espera)
	for {
		if conn, err := net.DialTimeout("tcp", dir, time.Second); err == nil {
			_ = conn.SetReadDeadline(time.Now().Add(time.Second))
			_, err := conn.Read(make([]byte, 1))
			conn.Close()
			// Mandó datos o la sostuvo sin hablar (protocolos donde empieza el cliente): escucha.
			var ne net.Error
			if err == nil || errors.As(err, &ne) && ne.Timeout() {
				return nil
			}
		}
		if time.Now().After(limite) {
			return fmt.Errorf("el puerto %d no aceptó conexiones en %s", puerto, espera)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
