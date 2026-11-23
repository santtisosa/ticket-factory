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

> **Estado del proyecto: versión 0.1, en desarrollo.** Probado principalmente en macOS; el soporte de Linux depende de
> `srt` y del sandbox de Claude Code con bubblewrap.

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
| **Chequeo** | Ejecuta el comando de verificación del repositorio dentro de `srt`, sin red a GitHub, con los servicios que el proyecto declare. Si falla, el Builder retoma su sesión con la salida completa, sin consumir una iteración. |
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
| [`srt`](https://github.com/anthropics/sandbox-runtime) (sandbox runtime de Anthropic) | Aísla el chequeo, el hook de cierre del Builder, la vista previa y las herramientas del escaneo. Se instala con `npm install -g @anthropic-ai/sandbox-runtime`. Sin `srt`, la tarea se detiene como falla de infraestructura y no se ejecuta código de la rama. | Sí |
| [Claude Code](https://code.claude.com) | Motor de cada etapa. Se instala por separado y se autentica con la cuenta del usuario. | Sí |
| Go 1.26+ | Compilación. | Sí |
| git | Clones, worktrees y commits. | Sí |
| [GitHub CLI](https://cli.github.com) | Pull requests y CI. gh no necesita sesión: la fábrica usa su propio token. | Sí, para publicar y seguir PRs |
| `FABRICA_GITHUB_TOKEN` | Token fine-grained propio de la fábrica, limitado a los repositorios de `fabrica.yaml` (permisos en [docs/seguridad.md](docs/seguridad.md)). En el entorno o en `~/.config/ticket-factory/env` (permisos 600). La fábrica no usa las credenciales de GitHub del usuario. | Sí, para publicar y seguir PRs |
| Docker | Servicios de prueba declarados en `fabrica.yaml`. La fábrica los levanta fuera del encierro; el código de la rama no tiene acceso a Docker. | No |
| Node.js | Revisores de UX/UI (Playwright). | No |
| gitleaks, semgrep, gosec, govulncheck | Análisis estático adicional. Si alguno falta, la revisión lo informa como no ejecutado. | No |

## Instalación

```bash
git clone https://github.com/santtisosa/ticket-factory.git
cd ticket-factory
make build
./fabrica doctor
```

`fabrica doctor` verifica que estén git, Claude Code, `srt`, Docker (requerido solo si algún proyecto declara
`servicios`) y GitHub CLI; que `FABRICA_GITHUB_TOKEN` esté definido, y que `fabrica.yaml` sea válido y los archivos
que declara (`reglas`, `diseno`, `conocimiento.repo` local) existan. Termina con error si falta algo requerido.

Para correr la fábrica como servicio de la sesión hay ejemplos en `contrib/macos/` (launchd) y `contrib/linux/`
(systemd).

## Inicio rápido

```bash
mkdir ~/espacio-fabrica && cd ~/espacio-fabrica
fabrica init                    # genera fabrica.yaml a partir del ejemplo
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
        entorno: { MYSQL_ROOT_PASSWORD: root }   # solo valores de prueba
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

reglas: reglas-del-equipo.md              # convenciones del equipo, incorporadas a los prompts
diseno: DISENO.md                         # guía visual para la revisión de UI

merge:
  automatico: false                       # true: integra cambios no sensibles con el check en verde
  check_requerido: ci
  sensibles: ["**/migrations/**", "**/auth/**", ".github/**"]

limites:
  por_llamada_usd: 3
  por_dia_usd: 20
```

La receta `preview` corre en el worktree del primer repositorio con `frontend: true` (o en el primero del proyecto), con
`{puerto}` reemplazado por un puerto libre de `127.0.0.1` y las variables de `servicios` en el entorno. Los comandos se
inician juntos y quedan corriendo; un paso previo se encadena con `&&` en el mismo comando (`npm ci && npm run dev`). La misma receta
levanta la aplicación que examinan los revisores de UX y UI; sin `preview`, esas revisiones quedan sin realizar. Si la
aplicación requiere iniciar sesión, el usuario de prueba se toma de `FABRICA_APP_USUARIO` y `FABRICA_APP_CLAVE`
(`FABRICA_APP_LOGIN` indica el endpoint JSON de inicio de sesión; por defecto, `/api/auth/login`).

`reglas` y `diseno` son archivos Markdown del equipo: el contenido de `reglas` se agrega a los prompts del Spec writer,
del Builder y de los revisores; el de `diseno`, a los de los revisores de UX y UI. `conocimiento` es opcional: sin él,
el Jefe (`fabrica preguntar`) y el Bibliotecario no corren. El Bibliotecario escribe solo en las rutas de
`escribibles` (relativas al repositorio; una carpeta termina en `/`), sobre un clon propio en la rama `cerebro`;
`fabrica cerebro-sync` lleva esos cambios al repositorio original como pull request en borrador.

## Comandos

| Comando | Descripción |
|---|---|
| `fabrica init` | Escribe `fabrica.yaml` a partir del ejemplo, si no existe |
| `fabrica doctor` | Verifica dependencias, token y `fabrica.yaml` |
| `fabrica help` | Lista los comandos |
| `fabrica add [--proyecto <p>] [--prioridad <n>] "<tarea>"` | Encola una tarea |
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
| `fabrica probar-app [<proyecto>]` | Levanta la aplicación desde main con la receta `preview`, la escanea y la detiene |
| `fabrica aprobar <id> [--seco]` | Publica el commit aprobado, abre el pull request y dispara el CI |
| `fabrica prs` | Estado de CI y merge de los pull requests abiertos |
| `fabrica mergeado <id>` | Marca como integrada una tarea ya llevada al main local (proyectos sin GitHub) |
| `fabrica desplegar <id> [--seco]` | Despliega una tarea integrada |
| `fabrica notificar <id> [--enviar] [--para <correo>] [--mensaje "<texto>"]` | Muestra o envía la notificación de una tarea en producción |
| `fabrica preguntar "<consulta>"` | Consulta la base de conocimiento, con citas verificadas |
| `fabrica indice` | Regenera el índice de la base de conocimiento |
| `fabrica bibliotecario <id>` | Incorpora a la base de conocimiento lo aprendido en una tarea cerrada |
| `fabrica lint-cerebro` | Busca contradicciones en la base de conocimiento (solo informa) |
| `fabrica cerebro-sync [--seco]` | Lleva lo aprendido al repositorio de conocimiento como pull request en borrador |
| `fabrica replay [prefijo]` · `metricas [prefijo]` | Reevalúa decisiones pasadas · costos y causas de bloqueo |
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
| Dependencias | npm y proxy de Go | Builder, chequeo y vista previa, con la misma lista cerrada de dominios (sin GitHub) |
| Reglas y bases de vulnerabilidades | semgrep, npm audit, govulncheck | Durante la revisión, si están instalados |
| Servidor MCP de Playwright | npm | Revisión de UX, en repositorios con `frontend: true` |
| Lectura de issues, proyectos y pull requests | GitHub | Lectura de fuentes y seguimiento |
| Etiquetas `fabrica:en-curso` y `fabrica:lista` en el issue de origen | GitHub | Al tomar la tarea y al dejarla lista (solo `github-issues`) |
| Publicación de la rama y pull request | GitHub | **Únicamente** con `fabrica aprobar` |
| Merge | GitHub | Solo con `merge.automatico` activo y el check requerido en verde |
| Despliegue | Según el comando configurado | Solo si existe `deploy` |
| Notificación | Servidor SMTP, GitHub o el comando configurado | Solo con `fabrica notificar --enviar` |

## Costos

El consumo de Claude Code se factura a la cuenta del usuario. Como referencia operativa, una tarea acotada insume
entre USD 3 y USD 15 y entre 20 y 30 minutos; una tarea con dos iteraciones, aproximadamente el doble. `fabrica
metricas` desglosa el costo por etapa y por tarea, y los límites de `fabrica.yaml` interrumpen la ejecución antes de
superarlos.

## Seguridad

- **Contenido no confiable.** El texto de un ticket puede contener instrucciones maliciosas. Los agentes lo reciben
  delimitado como datos, nunca como órdenes. Se recomienda restringir los autores admitidos con `autores`.
- **Sandbox obligatorio.** El Bash del Builder y el del revisor del resultado se ejecutan en el sandbox de Claude Code
  con `failIfUnavailable`, con settings por tarea: escriben solo en sus worktrees, en los clones de su tarea y en el
  temporal y las cachés de la tarea. Los demás agentes no tienen Bash libre; los contienen los permisos de Claude Code,
  no el sistema operativo: corren como el usuario y con su autenticación de Claude Code.
- **Código de la rama aislado.** Todo lo que ejecuta código de la rama (el chequeo, el hook de cierre del Builder y la
  aplicación de la vista previa y de la revisión de UX/UI) corre dentro de `srt` con un perfil por tarea: red únicamente
  a los registros de dependencias y a `localhost`, sin acceso a GitHub; escritura limitada a los worktrees, los clones y
  el temporal y las cachés de la tarea; sin lectura de credenciales, del llavero de macOS, de `~/Documents` fuera de la
  tarea, de `~/.claude` ni de los datos de otras aplicaciones; sin Apple Events ni Docker. Las herramientas del escaneo
  (gosec, govulncheck, semgrep, gitleaks, `npm audit`) corren en `srt` con un perfil propio, sin escribir los worktrees,
  y con la configuración de la fábrica: los ignores de la rama no esconden hallazgos. Antes de cada tarea, una sonda
  comprueba que dentro de `srt` GitHub no responda y el home no se escriba; si `srt` no está o no aísla, no se ejecuta
  nada. Un repositorio cuyo chequeo necesite contenedores debe declararlos en `servicios` de `fabrica.yaml`.
- **GitHub con un token propio.** Todo lo que la fábrica hace contra GitHub usa `FABRICA_GITHUB_TOKEN` y nunca el
  llavero, la sesión de gh ni las llaves ssh del usuario. El token solo llega a los procesos de gh y de git que hablan
  con GitHub; ni los agentes ni el código encerrado lo reciben. Se recomienda proteger `main` con una regla que exija el
  check requerido sin excepciones, para que el token no pueda integrar nada sin el CI.
- **Sin configuración plantada.** Los agentes corren con el directorio de trabajo en una carpeta propia de la fábrica,
  fuera de todo repo, que se verifica antes de cada llamada; los worktrees llegan solo como directorios adicionales.
- **Aislamiento de credenciales.** Ninguna variable de entorno con secretos llega a los agentes ni al código encerrado.
- **Riesgos residuales.** `localhost` queda abierto para el código encerrado, los registros de dependencias son una
  salida posible para datos de la tarea, el servidor MCP de Playwright, `ui-scan` y el despliegue corren fuera de todo
  encierro, y el despliegue usa las credenciales del usuario. La lista completa, con lo que está probado y lo que no,
  está en [docs/seguridad.md](docs/seguridad.md).

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
