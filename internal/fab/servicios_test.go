package fab

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func espacioConServicios(t *testing.T, servicios string) string {
	t.Helper()
	home := t.TempDir()
	yml := "proyectos:\n  demo:\n    repos:\n      - nombre: demo-api\n        ruta: /tmp/demo-api\n" + servicios
	if err := os.WriteFile(filepath.Join(home, ArchivoEspacio), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

// dockerFalso anota las llamadas; `inspect` falla (no existe) salvo que se indique la imagen actual.
func dockerFalso(t *testing.T, puerto int, actual string) *[]string {
	t.Helper()
	viejo := docker
	t.Cleanup(func() { docker = viejo })
	var llamadas []string
	docker = func(_ context.Context, args ...string) ([]byte, error) {
		llamadas = append(llamadas, strings.Join(args, " "))
		switch args[0] {
		case "inspect":
			if actual == "" {
				return []byte("Error: No such object"), fmt.Errorf("exit 1")
			}
			return []byte(actual), nil
		case "port":
			return []byte(fmt.Sprintf("127.0.0.1:%d\n[::1]:%d\n", puerto, puerto)), nil
		}
		return nil, nil
	}
	return &llamadas
}

func TestServiciosSinDeclarar(t *testing.T) {
	llamadas := dockerFalso(t, 1, "")
	c := Config{Home: espacioConServicios(t, "")}
	env, err := c.asegurarServicios(context.Background(), []string{"demo-api"})
	if err != nil || env != nil || len(*llamadas) != 0 {
		t.Fatalf("sin servicios no se llama a docker: env=%v err=%v llamadas=%v", env, err, *llamadas)
	}
}

func TestServiciosLevantaYExporta(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() { // sostiene las conexiones sin hablar, como un servidor donde empieza el cliente
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { time.Sleep(3 * time.Second); conn.Close() }()
		}
	}()
	puerto := ln.Addr().(*net.TCPAddr).Port
	llamadas := dockerFalso(t, puerto, "")
	c := Config{Home: espacioConServicios(t, "    servicios:\n      - imagen: postgres:16\n        puerto: 5432\n"+
		"        exporta: { DB_URL: \"postgres://x@127.0.0.1:{puerto}/db\", DB_PUERTO: \"{puerto}\" }\n")}
	env, err := c.asegurarServicios(context.Background(), []string{"demo-api"})
	if err != nil {
		t.Fatal(err)
	}
	quiero := []string{fmt.Sprintf("DB_PUERTO=%d", puerto), fmt.Sprintf("DB_URL=postgres://x@127.0.0.1:%d/db", puerto)}
	if strings.Join(env, " ") != strings.Join(quiero, " ") {
		t.Fatalf("env %v, quería %v", env, quiero)
	}
	todo := strings.Join(*llamadas, "\n")
	huella := huellaServicio(Servicio{Imagen: "postgres:16", Puerto: 5432})
	if !strings.Contains(todo, "run -d --name fabrica-demo-0 --label fabrica.config="+huella+" -p 127.0.0.1::5432 postgres:16") || !strings.Contains(todo, "port fabrica-demo-0 5432/tcp") {
		t.Fatalf("llamadas: %s", todo)
	}
}

// Un contenedor que ya existe con la misma configuración se reusa; con otra (imagen, puerto o entorno) se recrea.
func TestServiciosReusaORecrea(t *testing.T) {
	viejo := puertoListo
	t.Cleanup(func() { puertoListo = viejo })
	puertoListo = func(context.Context, int, time.Duration) error { return nil }
	yml := "    servicios:\n      - imagen: redis:7\n        puerto: 6379\n"
	misma := huellaServicio(Servicio{Imagen: "redis:7", Puerto: 6379})
	otra := huellaServicio(Servicio{Imagen: "redis:7", Puerto: 6379, Entorno: map[string]string{"X": "1"}})
	if misma == otra {
		t.Fatal("el entorno tiene que cambiar la huella")
	}
	for _, c := range []struct{ actual, quiere, noQuiere string }{
		{misma, "start fabrica-demo-0", "rm -f"},
		{otra, "rm -f fabrica-demo-0", "start"},
	} {
		llamadas := dockerFalso(t, 40000, c.actual)
		cfg := Config{Home: espacioConServicios(t, yml)}
		if _, err := cfg.asegurarServicios(context.Background(), []string{"demo-api"}); err != nil {
			t.Fatal(err)
		}
		todo := strings.Join(*llamadas, "\n")
		if !strings.Contains(todo, c.quiere) || strings.Contains(todo, c.noQuiere) {
			t.Errorf("%s: %q", c.actual, *llamadas)
		}
	}
}

// El entorno del servicio llega al contenedor como -e, en orden estable.
func TestServiciosEntorno(t *testing.T) {
	viejo := puertoListo
	t.Cleanup(func() { puertoListo = viejo })
	puertoListo = func(context.Context, int, time.Duration) error { return nil }
	llamadas := dockerFalso(t, 40000, "")
	c := Config{Home: espacioConServicios(t, "    servicios:\n      - imagen: mysql:8.4\n        puerto: 3306\n"+
		"        entorno: { MYSQL_ROOT_PASSWORD: root, MYSQL_DATABASE: app }\n")}
	if _, err := c.asegurarServicios(context.Background(), []string{"demo-api"}); err != nil {
		t.Fatal(err)
	}
	if todo := strings.Join(*llamadas, "\n"); !strings.Contains(todo, "-e MYSQL_DATABASE=app -e MYSQL_ROOT_PASSWORD=root mysql:8.4") {
		t.Fatalf("llamadas: %s", todo)
	}
}

func TestServiciosDockerCaido(t *testing.T) {
	viejo := docker
	t.Cleanup(func() { docker = viejo })
	docker = func(context.Context, ...string) ([]byte, error) {
		return []byte("Cannot connect to the Docker daemon"), fmt.Errorf("exit 1")
	}
	c := Config{Home: espacioConServicios(t, "    servicios:\n      - imagen: redis:7\n        puerto: 6379\n")}
	if _, err := c.asegurarServicios(context.Background(), []string{"demo-api"}); err == nil || !strings.Contains(err.Error(), "Docker daemon") {
		t.Fatalf("err=%v", err)
	}
}

// Una conexión que se cierra al instante (el proxy de Docker sin servicio detrás) no cuenta.
func TestEsperarPuertoCierreInmediato(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	err = esperarPuerto(context.Background(), ln.Addr().(*net.TCPAddr).Port, 1500*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "no aceptó conexiones") {
		t.Fatalf("err=%v", err)
	}
}

func TestPuertoPublicado(t *testing.T) {
	if p, err := puertoPublicado("0.0.0.0:55001\n:::55001\n"); p != 55001 || err != nil {
		t.Fatalf("%d %v", p, err)
	}
	if _, err := puertoPublicado(""); err == nil {
		t.Fatal("vacío es un error")
	}
}

// Un contenedor que sale al arrancar (una imagen sin la variable que exige) deja su log en el motivo.
func TestServiciosMotivoConElLog(t *testing.T) {
	viejo := docker
	t.Cleanup(func() { docker = viejo })
	docker = func(_ context.Context, args ...string) ([]byte, error) {
		switch {
		case args[0] == "inspect" && strings.Contains(args[2], "State"):
			return []byte("exited (salida 1)"), nil
		case args[0] == "inspect":
			return nil, fmt.Errorf("no existe")
		case args[0] == "port":
			return []byte("Error: no public port"), fmt.Errorf("exit 1")
		case args[0] == "logs":
			return []byte("Database is uninitialized and password option is not specified"), nil
		}
		return nil, nil
	}
	c := Config{Home: espacioConServicios(t, "    servicios:\n      - imagen: mysql:8.4\n        puerto: 3306\n")}
	_, err := c.asegurarServicios(context.Background(), []string{"demo-api"})
	if err == nil || !strings.Contains(err.Error(), "exited (salida 1)") || !strings.Contains(err.Error(), "password option") {
		t.Fatalf("err=%v", err)
	}
}
