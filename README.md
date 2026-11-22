```
╔═══════════════════════════════════════════════════════════════════════════╗
║                                                                           ║
║               ████████╗██╗ ██████╗██╗  ██╗███████╗████████╗               ║
║               ╚══██╔══╝██║██╔════╝██║ ██╔╝██╔════╝╚══██╔══╝               ║
║                  ██║   ██║██║     █████╔╝ █████╗     ██║                  ║
║                  ██║   ██║██║     ██╔═██╗ ██╔══╝     ██║                  ║
║                  ██║   ██║╚██████╗██║  ██╗███████╗   ██║                  ║
║                  ╚═╝   ╚═╝ ╚═════╝╚═╝  ╚═╝╚══════╝   ╚═╝                  ║
║                                                                           ║
║        ███████╗ █████╗  ██████╗████████╗ ██████╗ ██████╗ ██╗   ██╗        ║
║        ██╔════╝██╔══██╗██╔════╝╚══██╔══╝██╔═══██╗██╔══██╗╚██╗ ██╔╝        ║
║        █████╗  ███████║██║        ██║   ██║   ██║██████╔╝ ╚████╔╝         ║
║        ██╔══╝  ██╔══██║██║        ██║   ██║   ██║██╔══██╗  ╚██╔╝          ║
║        ██║     ██║  ██║╚██████╗   ██║   ╚██████╔╝██║  ██║   ██║           ║
║        ╚═╝     ╚═╝  ╚═╝ ╚═════╝   ╚═╝    ╚═════╝ ╚═╝  ╚═╝   ╚═╝           ║
║                                                                           ║
╟───────────────────────────────────────────────────────────────────────────╢
║    ticket  ─▶  spec  ─▶  código  ─▶  revisión  ─▶  aprobación  ─▶  PR     ║
╚═══════════════════════════════════════════════════════════════════════════╝
```

<p align="center"><b>Orquestador de agentes que lleva un ticket desde la descripción hasta una rama revisada, probada y lista para aprobar.</b></p>

<p align="center">
  <a href="#instalación">Instalación</a> ·
  <a href="#inicio-rápido">Inicio rápido</a> ·
  <a href="#configuración">Configuración</a> ·
  <a href="#comandos">Comandos</a> ·
  <a href="#seguridad">Seguridad</a>
</p>

> **Estado del proyecto: en desarrollo.** ticket-factory se usa en producción desde octubre de 2026 sobre repositorios
> reales y se está generalizando para su distribución pública. Este repositorio contiene por ahora la documentación de
> la versión 0.1; el código se publica una vez superadas las pruebas de integración en Linux.

---

## Descripción

ticket-factory recibe un ticket (un issue de GitHub, una tarjeta de un tablero o una descripción por línea de comandos)
y lo procesa a través de una línea de etapas especializadas: especificación, implementación, pruebas, revisión
múltiple y decisión. El resultado es una rama con el cambio implementado, sus pruebas de aceptación y un informe de
revisión, a la espera de aprobación humana.

Cada etapa es una ejecución aislada de [Claude Code](https://code.claude.com) en modo no interactivo (`claude -p`), con
su propio prompt, herramientas habilitadas y esquema de salida. La orquestación la realiza un proceso en Go
determinista: no interpreta, no improvisa y no aprueba cambios por su cuenta. Ningún cambio sale de la máquina local
sin una aprobación explícita.

## Arquitectura

```
                                                          ┌───────────┐
                                                        ┌▶│ seguridad │─┐
┌─────────┐                ┌─────────────┐              │ └───────────┘ │
│   logs  │─┐              │   cerebro   │              │               │
└─────────┘ │              └──────┬──────┘              │ ┌───────────┐ │                                ┌╌╌╌╌╌╌┐
            │                     │                     ├▶│     ux    │─┤                              ┌▶╎ main ╎─┐
            │                     │                     │ └───────────┘ │                              │ ╎manual╎ │
            │                     ▼                     │               │                              │ └╌╌╌╌╌╌┘ │
┌─────────┐ │ ┌─────────┐  ┌─────────────┐  ┌─────────┐ │ ┌───────────┐ │ ┌──────────┐  ┌────────────┐ │          │ ┌╌╌╌╌╌╌┐
│ tickets │─┼▶│ entrada │─▶│ spec writer │─▶│ builder │─┼▶│     ui    │─┼▶│ decisión │─▶│ aprobación │─┤          ├▶╎ prod ╎
└─────────┘ │ └─────────┘  └─────────────┘  └─────────┘ │ └───────────┘ │ └─────┬────┘  └────────────┘ │          │ ╎manual╎
            │                                    ▲      │               │       │                      │          │ └╌╌╌╌╌╌┘
            │                                    │      │ ┌───────────┐ │       │                      │ ┌──────┐ │
            │                                    │      ├▶│   código  │─┤       │                      └▶│  PR  │─┘
┌─────────┐ │                                    │      │ └───────────┘ │       │                        └──────┘
│  prompt │─┘                                    │      │               │       │
└─────────┘                                      │      │ ┌───────────┐ │       │
                                                 │      └▶│ resultado │─┘       │
                                                 │        └───────────┘         │
                                                 │                              │
                                                 └ reintento con hallazgos · ≤2 ┘
```

| Etapa | Responsabilidad |
|---|---|
| **Entrada** | Normaliza lo que llega, elimina duplicados y enmascara datos personales (correos, teléfonos, credenciales) antes de que los procese un modelo. Lo que proviene de logs o de terceros ingresa como `PROPUESTA` y requiere aceptación. |
| **Spec writer** | Redacta una especificación de una página con criterios de aceptación numerados (`CA-1`, `CA-2`, …). Si detecta una ambigüedad que afecta el alcance, se detiene y la plantea en lugar de suponer. |
| **Builder** | Implementa el cambio en un worktree aislado, dentro del sandbox de Claude Code: escritura limitada a la rama de la tarea, sin credenciales de publicación y con red restringida. Escribe una prueba por criterio. |
| **Chequeo** | Ejecuta el comando de verificación del repositorio fuera del sandbox, con los servicios que el proyecto declare. Si falla, el Builder retoma su sesión con la salida completa, sin consumir una iteración. |
| **Revisión** | Revisores independientes de seguridad y código; de UX y UI cuando el cambio afecta interfaces; y del resultado observable (pantallas, documentos, correos) cuando corresponde. Todo hallazgo exige evidencia `archivo:línea`. Se complementa con gitleaks, semgrep, gosec, govulncheck y `npm audit` cuando están instalados. |
| **Decisión** | Función determinista en Go. Aprueba si la implementación terminó, el chequeo pasó y no hay hallazgos graves con evidencia; de lo contrario devuelve al Builder la lista concreta, con un máximo de dos iteraciones. |
| **Aprobación** | Revisión humana de la rama, con vista previa opcional de la aplicación. `fabrica aprobar` publica exactamente el commit aprobado, abre el pull request y dispara el CI. |
| **Post-merge** | Opcional: despliegue mediante un comando configurable con verificación de salud, y notificación a quien originó el ticket. |
| **Conocimiento** | Opcional: una base de notas en Markdown que se consulta antes de especificar y se actualiza al cerrar cada tarea. Las respuestas citan archivo y línea verificables. |

## Principios de diseño

- **Aprobación humana obligatoria.** Los agentes procesan contenido no confiable (tickets, logs), por lo que ninguno
  dispone de credenciales de publicación. Toda acción externa la ejecuta el orquestador tras una aprobación explícita.
- **Decisión determinista.** Un modelo puede emitir hallazgos, pero no aprobar. La regla de decisión es código legible
  (`internal/fab/approver.go`) y reproducible sobre tareas anteriores (`fabrica replay`).
- **Trazabilidad completa.** Cada tarea conserva en `runs/<id>/` la entrada, la especificación, la salida de cada
  etapa, el costo y un registro de eventos (`events.jsonl`). El panel web en `http://127.0.0.1:7171` muestra el estado
  de la línea en tiempo real.
- **Costos acotados.** Límites configurables por llamada y por día.

## Requisitos

| Componente | Uso | Requerido |
|---|---|---|
| macOS o Linux | El sandbox de Claude Code utiliza Seatbelt (macOS) o bubblewrap (Linux). Windows nativo no está soportado. | Sí |
| [Claude Code](https://code.claude.com) | Motor de cada etapa. Se instala por separado y se autentica con la cuenta del usuario. `fabrica doctor` verifica la versión mínima. | Sí |
| Go 1.26+ | Compilación. | Sí |
| git y [GitHub CLI](https://cli.github.com) autenticado | Worktrees, pull requests y CI. | Sí |
| Docker | Servicios de prueba del proyecto, vista previa y revisión de UX/UI. | No |
| Node.js | Revisores de UX/UI (Playwright). | No |
| gitleaks, semgrep, gosec, govulncheck | Análisis estático adicional. Si alguno falta, la revisión lo informa como no ejecutado. | No |

## Instalación

```bash
git clone https://github.com/santtisosa/ticket-factory
cd ticket-factory
make build
./fabrica doctor
```

`fabrica doctor` verifica Claude Code, la autenticación, GitHub CLI, git, Docker y la disponibilidad del sandbox.

## Inicio rápido

```bash
mkdir ~/espacio-fabrica && cd ~/espacio-fabrica
fabrica init                    # genera fabrica.yaml y reglas-de-la-casa.md
fabrica add --proyecto demo "Agregar la versión de la aplicación al endpoint /health"
fabrica run --once              # procesa la tarea y finaliza
fabrica ver <id>                # detalle: etapas, costo, rama resultante
fabrica aprobar <id> --seco     # muestra qué se publicaría, sin publicar
fabrica aprobar <id>            # publica la rama y abre el pull request
```

## Configuración

La configuración vive en `fabrica.yaml`, uno por espacio de trabajo. Los secretos no se declaran en este archivo: se
leen de variables de entorno o de `~/.config/ticket-factory/env` (permisos `600`).

```yaml
proyectos:
  demo:
    repos:
      - nombre: demo-api
        ruta: git@github.com:organizacion/demo-api.git
        check: make check                 # verificación que debe pasar; si se omite, se infiere
        deploy: ./scripts/deploy.sh       # opcional, posterior al merge
        smoke: https://api.demo.example.com/health
      - nombre: demo-web
        ruta: git@github.com:organizacion/demo-web.git
        check: npm run lint && npm test
        frontend: true                    # habilita la revisión de UX y UI
    servicios:                            # opcional: dependencias de las pruebas (Docker)
      - imagen: mysql:8.4
        puerto: 3306
        exporta: { TESTDB_DSN: "root:root@tcp(127.0.0.1:{puerto})/" }
    preview:                              # opcional: vista previa de la rama antes de aprobar
      comandos: ["npm run dev -- --port {puerto}"]
    notificacion:
      para: solicitante@example.com

fuentes:
  github-issues:
    repo: organizacion/demo-api
    etiqueta: fabrica                     # solo issues con esta etiqueta
    autores: [usuario-autorizado]         # y solo de estos autores
  betterstack: {}                         # incidentes de producción, como PROPUESTA
  # exec: integración con cualquier sistema mediante un comando que emite JSON (docs/fuentes.md)

notificacion:
  canal: smtp                             # smtp | comentario | exec (servidor en FABRICA_SMTP_*)
  remitente: fabrica@example.com

conocimiento:
  repo: ~/notas-del-equipo                # repositorio git de Markdown
  escribibles: ["lecciones.md", "modulos/"]

reglas: reglas-de-la-casa.md              # convenciones del equipo, incorporadas a los prompts
diseno: DISENO.md                         # guía visual para la revisión de UI

merge:
  automatico: false                       # true: integra cambios no sensibles con el check en verde
  check_requerido: ci
  sensibles: ["**/migrations/**", "**/auth/**", ".github/**"]

limites:
  por_llamada_usd: 3
  por_dia_usd: 20
```

## Comandos

| Comando | Descripción |
|---|---|
| `fabrica init` | Genera la configuración inicial del espacio de trabajo |
| `fabrica doctor` | Verifica dependencias, autenticación y sandbox |
| `fabrica add [--proyecto <p>] "<tarea>"` | Encola una tarea |
| `fabrica status` | Lista las tareas y su estado |
| `fabrica ver <id>` | Detalle de una tarea: origen, eventos, costo y artefactos |
| `fabrica run [--once]` | Procesa la cola (una tarea por proyecto en simultáneo) |
| `fabrica serve [--abrir]` | Procesamiento, lectura de fuentes y panel web en un solo proceso |
| `fabrica web [--abrir]` | Solo el panel web |
| `fabrica ingestar` | Lee las fuentes configuradas una vez |
| `fabrica aceptar <id>` · `descartar <id>` | Resuelve una `PROPUESTA` |
| `fabrica continuar <id> "<respuestas>"` | Reanuda una tarea detenida en la especificación |
| `fabrica reintentar <id>` · `cancelar <id>` | Reencola una tarea `TRABADA` o la descarta |
| `fabrica prioridad <id> <n>` | Ajusta la prioridad en la cola |
| `fabrica pausar` · `seguir` | Suspende o reanuda la toma de tareas |
| `fabrica preview <id>` · `preview bajar` | Levanta o detiene la vista previa de una tarea |
| `fabrica aprobar <id> [--seco]` | Publica el commit aprobado, abre el pull request y dispara el CI |
| `fabrica prs` | Estado de CI y merge de los pull requests abiertos |
| `fabrica desplegar <id> [--seco]` | Despliega una tarea integrada |
| `fabrica notificar <id> [--enviar]` | Muestra o envía la notificación de una tarea en producción |
| `fabrica preguntar "<consulta>"` | Consulta la base de conocimiento, con citas verificadas |
| `fabrica aprender <ruta>` | Incorpora una carpeta de referencia a la base de conocimiento |
| `fabrica replay` · `metricas` | Reevalúa decisiones pasadas · costos y causas de bloqueo |
| `fabrica limpiar` | Elimina los worktrees de tareas cerradas |

## Ciclo de vida de una tarea

```
PROPUESTA ─aceptar─▶ PENDIENTE ─▶ EN_CURSO ─▶ ESPERANDO_APROBACION ─aprobar─▶ PR_ABIERTO ─▶ CI_VERDE ─▶ INTEGRADA
                                     │                                                                     │
                                     └─▶ TRABADA (con motivo; reintentar o continuar)                     ▼
                                                                                                 DESPLIEGUE_PENDIENTE
```

## Tráfico de red

| Datos | Destino | Momento |
|---|---|---|
| Prompts y código del repositorio | Anthropic | En cada etapa |
| Dependencias | npm y proxy de Go | Builder (lista cerrada de dominios); el chequeo se ejecuta fuera del sandbox con red abierta |
| Reglas y bases de vulnerabilidades | semgrep, npm audit, govulncheck | Durante la revisión, si están instalados |
| Servidor MCP de Playwright | npm | Revisión de UX, en repositorios con `frontend: true` |
| Lectura de issues, proyectos y pull requests | GitHub | Lectura de fuentes y seguimiento |
| Publicación de la rama y pull request | GitHub | **Únicamente** con `fabrica aprobar` |
| Merge | GitHub | Solo con `merge.automatico` activo y el check requerido en verde |
| Despliegue | Según el comando configurado | Solo si existe `deploy` |
| Notificación | Servidor SMTP o GitHub | Solo con `fabrica notificar --enviar` |

## Costos

El consumo de Claude Code se factura a la cuenta del usuario. Como referencia operativa, una tarea acotada insume
entre USD 3 y USD 15 y entre 20 y 30 minutos; una tarea con dos iteraciones, aproximadamente el doble. `fabrica
metricas` desglosa el costo por etapa y por tarea, y los límites de `fabrica.yaml` interrumpen la ejecución antes de
superarlos.

## Seguridad

- **Contenido no confiable.** El texto de un ticket puede contener instrucciones maliciosas. Los agentes lo reciben
  delimitado como datos, nunca como órdenes. Se recomienda restringir los autores admitidos con `autores`.
- **Sandbox obligatorio.** El Builder se ejecuta en el sandbox de Claude Code con `failIfUnavailable`: si el sandbox no
  está disponible, la tarea no se ejecuta.
- **Chequeo fuera del sandbox.** La verificación ejecuta el código del repositorio y los cambios del Builder con acceso
  a Docker y a la red. Se recomienda operar sobre repositorios de confianza, en una máquina o VM dedicada.
- **Aislamiento de credenciales.** Ninguna variable de entorno con secretos llega a los agentes, que tampoco pueden
  leer `~/.ssh`, `~/.aws`, `~/.config/gh` ni el archivo de credenciales de la herramienta.

## Claude Code y autenticación

ticket-factory ejecuta Claude Code sin incluirlo ni modificarlo; cada usuario lo instala y autentica con su propia
cuenta. Para uso automatizado, la documentación de Anthropic recomienda una API key de la
[Claude Console](https://platform.claude.com) o un proveedor compatible (Amazon Bedrock, Google Vertex AI, Microsoft
Foundry). Quien utilice una suscripción debe revisar previamente los
[términos aplicables](https://code.claude.com/docs/en/legal-and-compliance). Cada usuario es responsable de las
acciones de los agentes que ejecuta.

ticket-factory es un proyecto independiente, sin afiliación con Anthropic.

## Limitaciones conocidas

- Integración con GitHub únicamente; GitLab y otros proveedores no están soportados.
- Una tarea en ejecución por proyecto.
- La revisión de UX/UI requiere que el proyecto pueda levantarse mediante un comando.
- Interfaz, comandos y prompts en español.

## Licencia

Distribuido bajo licencia [MIT](LICENSE).
