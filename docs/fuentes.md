# Fuentes de tickets

Las fuentes se declaran en `fuentes` de `fabrica.yaml`. Solo se lee lo que está declarado; sin
`fabrica.yaml` no se lee ninguna. `fabrica ingestar` las lee una vez y `fabrica serve` cada 10 minutos.
Cada elemento se carga una sola vez (se deduplica por su clave) y su texto se enmascara antes de llegar
a un modelo.

Un elemento entra como `PENDIENTE` (directo a la cola) o como `PROPUESTA` (espera `fabrica aceptar`).

## github-issues

```yaml
fuentes:
  github-issues:
    repo: organizacion/demo-api
    etiqueta: fabrica
    autores: [usuario-autorizado]
    proyecto: demo          # opcional
```

Lee los issues abiertos con la etiqueta mediante `gh issue list`. Solo los de un autor de `autores`
entran como `PENDIENTE`; con `autores` vacío, todos entran como `PROPUESTA`. La referencia del issue
(`organizacion/demo-api#12`) queda en el campo `origen` de `00-entrada.json`.

Seguimiento: al tomar la tarea, el orquestador agrega al issue la etiqueta `fabrica:en-curso`; cuando la
rama queda lista para la aprobación, agrega `fabrica:lista` y quita `fabrica:en-curso`. Ambas etiquetas
tienen que existir en el repositorio (`gh label create fabrica:en-curso`, `gh label create fabrica:lista`).
Si la edición falla, se registra en los eventos de la tarea y el trabajo sigue.

## github-projects

```yaml
fuentes:
  github-projects:
    owner: organizacion
    numero: 1
    columna: Todo           # por defecto "Todo"
    prefijo: "fabrica:"     # por defecto "fabrica:"
    autores: [usuario1]     # opcional
```

Lee las tarjetas de la columna cuyo título empieza con el prefijo (sin distinguir mayúsculas). Los
borradores del tablero entran como `PENDIENTE`: solo los crea quien puede editarlo. Un issue o una pull
request agregados al tablero entran como `PENDIENTE` solo si su autor está en `autores`; el resto, como
`PROPUESTA`. La fábrica lee `content.type` y `content.author` de `gh project item-list --format json`; si la versión
de gh no los trae, la tarjeta queda en `PROPUESTA`.

## betterstack

```yaml
fuentes:
  betterstack: {}
```

Incidentes abiertos o de las últimas 24 horas de Better Stack Uptime. El token se lee de
`BETTERSTACK_TOKEN`; sin token, la fuente figura como sin configurar. Siempre entran como `PROPUESTA`.

## exec

Integra cualquier sistema mediante un comando que imprime los tickets en JSON.

```yaml
fuentes:
  exec:
    - nombre: mesa-de-ayuda
      comando: ./scripts/tickets.sh
```

El comando se ejecuta con `bash -c`, en la carpeta de `fabrica.yaml`, con un tope de 2 minutos. Por
la salida estándar tiene que imprimir una lista JSON:

```json
[
  {"clave": "4512", "texto": "El listado de pedidos no ordena por fecha", "listo": false, "servicio": "demo-web"}
]
```

| Campo | Obligatorio | Significado |
|---|---|---|
| `clave` | Sí | Identificador estable del ticket en su sistema. La clave final es `<nombre>:<clave>` |
| `texto` | Sí | Descripción del pedido |
| `listo` | No | `true` lo carga como `PENDIENTE`; por defecto, `PROPUESTA` |
| `servicio` | No | Nombre del servicio o proyecto; `demo` o `demo-web` se asignan al proyecto `demo` |

Una salida que no es una lista JSON, o un elemento sin `clave` o sin `texto`, hace fallar la lectura
completa de esa fuente con un error que la identifica. Una salida con código distinto de 0 también.

El comando **no hereda los secretos** del entorno de la fábrica: recibe solo `PATH`, `HOME` y variables
básicas del sistema. Si necesita una credencial, la lee él mismo (por ejemplo, de un archivo con permisos
`600`). Decidir qué entra como `listo: true` es responsabilidad del comando: su texto llega al Builder.

Ejemplo de script que lee una API con `curl` y `jq`:

```bash
#!/usr/bin/env bash
set -euo pipefail
token=$(cat ~/.config/mesa-de-ayuda/token)
curl -fsS -H "Authorization: Bearer $token" https://mesa.example.com/api/tickets?estado=abierto |
  jq '[.tickets[] | {clave: (.id | tostring), texto: (.titulo + "\n\n" + .detalle), listo: false, servicio: .sistema}]'
```

# Notificación

`fabrica notificar <id>` muestra el borrador de la notificación de una tarea integrada y a quién iría;
`--enviar` la envía una sola vez por el canal de `notificacion.canal`:

| Canal | Envío | Destinatario |
|---|---|---|
| `smtp` | Servidor de `FABRICA_SMTP_HOST`, `FABRICA_SMTP_PUERTO` (por defecto 587, con STARTTLS si el servidor lo ofrece), `FABRICA_SMTP_USUARIO` y `FABRICA_SMTP_CLAVE`, desde `notificacion.remitente` | `--para` o `proyectos.<p>.notificacion.para` |
| `comentario` | `gh issue comment` sobre el issue de origen | Solo tareas que vinieron de `github-issues` |
| `exec` | `notificacion.comando` con el mensaje por la entrada estándar y `FABRICA_TAREA` y `FABRICA_PARA` en el entorno, sin los secretos de la fábrica | `--para` o `proyectos.<p>.notificacion.para` (puede quedar vacío) |

El puerto 465 (TLS implícito) no está soportado. El borrador lo redacta un agente y se le quitan enlaces,
correos y teléfonos; `--mensaje "<texto>"` lo reemplaza por un texto escrito por el operador.
