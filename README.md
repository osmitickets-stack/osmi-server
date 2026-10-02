# OSMI Server

Backend principal de **OSMI Tickets**, responsable de la lógica de negocio, persistencia y cumplimiento de órdenes de boletos.

Este módulo está implementado en **Go** y expone servicios **gRPC** consumidos por `osmi-gateway`. La API HTTP pública no vive aquí: el Gateway traduce las rutas REST definidas en Protobuf hacia este servicio.

---

## Responsabilidades

`osmi-server` concentra la lógica de aplicación de OSMI:

- clientes y compradores invitados;
- eventos y tipos de boleto;
- órdenes e items de orden;
- reserva y confirmación de inventario;
- tickets digitales;
- pagos con Stripe;
- procesamiento de webhooks;
- cumplimiento de órdenes pagadas;
- expiración de reservas;
- generación de QR;
- envío transaccional de boletos por email;
- acceso a PostgreSQL;
- integración con Redis cuando aplica.

La definición de contratos gRPC/HTTP pertenece al módulo independiente `osmi-protobuf`.

---

## Stack

- **Go**
- **gRPC**
- **Protocol Buffers**
- **PostgreSQL / PostGIS**
- **Redis**
- **Stripe**
- **Amazon SES vía SMTP**
- **Docker**

---

## Arquitectura

OSMI separa claramente las responsabilidades comerciales:

```text
USER
├── autenticación
├── roles
├── permisos
└── perfil

CUSTOMER
├── comprador
├── facturación
├── CRM
└── puede existir sin una cuenta de usuario

TICKET
└── pertenece comercialmente al customer
```

Una compra invitada no requiere un `user`.

El checkout debe poder:

1. crear o localizar un `customer` por los datos de compra;
2. crear la orden;
3. reservar inventario;
4. crear tickets `RESERVED`;
5. cobrar mediante Stripe;
6. confirmar la venta mediante webhook;
7. convertir los tickets a `SOLD`;
8. completar la orden;
9. enviar los boletos al correo utilizado en la compra.

---

## Flujo canónico de compra

Existe un único flujo público de fulfillment para una orden pagada.

```text
Frontend
   │
   ▼
CreateOrder
   │
   ├── BEGIN
   ├── bloquear inventario necesario
   ├── reservar inventario
   ├── crear Order
   ├── crear OrderItems
   ├── crear Tickets RESERVED
   └── COMMIT
   │
   ▼
PaymentService.CreatePayment
   │
   ▼
Stripe PaymentIntent
   │
   ▼
payment_intent.succeeded
   │
   ▼
POST /v1/webhooks/stripe
   │
   ▼
PaymentService.HandleWebhook
   │
   ├── validar firma Stripe
   ├── registrar/deduplicar event_id
   ├── actualizar Payment
   ├── actualizar estado de pago de Order
   └── ProcessPaidOrder
            │
            ├── BEGIN
            ├── LOCK Order
            ├── LOCK Tickets
            ├── validar estado/cantidades/tipos
            ├── RESERVED → SOLD
            ├── reserved_quantity -= N
            ├── sold_quantity += N
            ├── Order → COMPLETED
            └── COMMIT
                  │
                  ▼
             enviar email
```

### Regla de autoridad

Para el checkout público:

- `OrderService` es responsable de **reservar**.
- `PaymentService` es responsable de **confirmar una venta pagada**.
- `ProcessPaidOrder` es la operación que coordina ticket, inventario y orden después del pago.

No deben existir rutas públicas alternativas que fabriquen un ticket `SOLD` sin demostrar que la orden correspondiente fue pagada.

---

## Máquina de estados principal

```text
AVAILABLE
   │
   │ reserva
   ▼
RESERVED
   │
   ├── pago confirmado ─────────────► SOLD
   │                                  │
   │                                  └── ProcessPaidOrder
   │
   └── reserva vencida ─────────────► EXPIRED
                                      │
                                      └── libera inventario reservado
```

La transición financiera crítica es:

```text
Order PENDING + Payment PAID + Tickets RESERVED
                    │
                    ▼
             ProcessPaidOrder
                    │
                    ▼
Order COMPLETED + Tickets SOLD + inventario confirmado
```

---

## Idempotencia de Stripe

Stripe puede entregar el mismo evento más de una vez.

El servidor mantiene una barrera de idempotencia basada en el `event_id` de Stripe y en el estado persistido de la orden.

Comportamiento esperado:

```text
primer payment_intent.succeeded
    ↓
procesar una vez
    ↓
tickets SOLD
inventario confirmado
order COMPLETED

mismo evento otra vez
    ↓
reconocer como procesado
    ↓
sin ticket adicional
sin segunda mutación de inventario
sin segunda finalización de orden
sin segundo email
```

La existencia de un evento registrado no equivale por sí sola a que haya terminado correctamente; el estado de procesamiento persistido es la fuente de verdad.

---

## Consistencia transaccional

Las operaciones críticas de fulfillment se realizan dentro de una única transacción PostgreSQL.

`ProcessPaidOrder` protege la orden mediante lock y valida antes de mutar:

- estado del pago;
- estado de la orden;
- tickets asociados;
- cantidades esperadas;
- tipos de boleto;
- inventario reservado.

El objetivo es evitar:

- doble venta;
- overselling;
- tickets vendidos sin pago;
- desincronización entre tickets e inventario;
- órdenes completadas parcialmente.

---

## Expiración de reservas

Una reserva vencida debe liberar exactamente el inventario que consumió.

```text
RESERVED
   │
   ▼
EXPIRED
   │
   └── reserved_quantity -= cantidad liberada
```

La expiración es independiente del fulfillment de Stripe y no debe convertirse en una segunda autoridad global sobre los contadores de inventario.

---

## Email de boletos

El correo se envía **después del commit financiero**.

Esto es intencional: una falla SMTP no debe revertir una venta ya confirmada.

El email de una orden puede contener múltiples boletos y cada boleto puede incluir:

- evento;
- tipo de boleto;
- código;
- fecha;
- ubicación;
- QR.

Proveedor actual:

- **Amazon SES**
- conexión mediante **SMTP**

> Los secretos SMTP nunca deben versionarse.

---

## Confirmación de compra

La confirmación que consume el frontend se obtiene mediante el contrato:

```text
GET /v1/orders/{order_id}/confirmation?payment_intent_id={pi_id}
```

El servidor valida que el PaymentIntent corresponda a la orden solicitada antes de devolver información de confirmación.

La respuesta incluye información comercial de la compra, pero no debe exponer secretos internos del ticket.

---

## PostgreSQL

La base de datos es administrada por el módulo independiente `osmi-db`.

`osmi-server`:

- consume el esquema;
- ejecuta consultas y transacciones;
- no es la fuente oficial de migraciones.

La creación reproducible del esquema debe provenir de las migraciones oficiales de `osmi-db`.

---

## Tests

Comandos principales:

```bash
go test ./...
go build ./...
```

Los tests de integración que utilizan PostgreSQL deben ejecutarse exclusivamente contra una base de pruebas dedicada.

Nunca deben apuntar a la base productiva por conveniencia.

---

## Ejecución local

Desde el repositorio `osmi-server`:

```bash
go test ./...
go build ./...
```

Construcción Docker desde el directorio padre `osmi-back`:

```bash
docker build -t osmi-server:local-corrected ./osmi-server
```

En el stack local, el override `compose.local-corrected.yml` permite usar la imagen construida localmente.

Ese archivo es **exclusivo del entorno local** y no forma parte del despliegue de EC2.

---

## Puertos

| Servicio | Puerto |
|---|---:|
| gRPC server | `50051` |
| Health check | `8081` |

El tráfico HTTP público entra por `osmi-gateway`, no directamente por este servicio.

---

## Variables de entorno

El servicio utiliza variables de entorno para su configuración.

### Aplicación

```env
ENVIRONMENT=
LOG_LEVEL=
GRPC_PORT=
```

### PostgreSQL

```env
DATABASE_URL=
DB_HOST=
DB_PORT=
DB_NAME=
DB_USER=
DB_PASSWORD=
DB_SSLMODE=
```

### Redis

```env
REDIS_URL=
REDIS_PASSWORD=
REDIS_DB=
```

### Stripe

```env
STRIPE_SECRET_KEY=
STRIPE_WEBHOOK_SECRET=
```

### SMTP / Amazon SES

```env
SMTP_HOST=
SMTP_PORT=
SMTP_USERNAME=
SMTP_PASSWORD=
SMTP_FROM=
```

Nunca incluir valores reales en el repositorio, documentación, logs o issues.

---

## Seguridad

Principios aplicados al flujo de compra:

- Stripe webhook es la fuente de verdad para confirmar el pago.
- El frontend no puede marcar una orden como pagada.
- Una ruta genérica de tickets no debe poder saltarse el flujo financiero.
- Los webhooks deben ser autenticados mediante firma.
- Los eventos Stripe deben procesarse de forma idempotente.
- Los secretos sólo se cargan desde variables de entorno.
- La confirmación pública de compra no expone credenciales del boleto.
- Las mutaciones financieras importantes utilizan transacciones y locks.

---

## Estado validado

Actualmente se ha comprobado localmente el happy path:

```text
Create customer
    ↓
Create order
    ↓
Create PaymentIntent
    ↓
Stripe payment_intent.succeeded
    ↓
Webhook 200
    ↓
Payment COMPLETED
    ↓
Order PAID / COMPLETED
    ↓
Ticket SOLD
    ↓
Inventory confirmed
    ↓
Confirmation endpoint 200
    ↓
Email enviado
```

También se ha desplegado la versión actual de `server` y `gateway` mediante imágenes de GHCR en EC2 y se ha comprobado la conectividad:

```text
Nginx → Gateway → Server → PostgreSQL
```

---

## Trabajo pendiente antes de considerar el backend completamente endurecido

El happy path está operativo, pero todavía existen áreas que deben cerrarse formalmente:

1. prueba explícita de redelivery del mismo evento Stripe;
2. pruebas de concurrencia para checkout e inventario;
3. revisión final de expiración concurrente;
4. entrega durable/reintentos de email después del commit;
5. validación E2E del payload del QR contra el flujo real de check-in;
6. optimización de latencia en lectura de eventos/tipos de boleto;
7. revisión final de rutas públicas y autorización;
8. observabilidad y alertas operativas.

Estas tareas no cambian la arquitectura canónica descrita arriba.

---

## Repositorios relacionados

Este módulo forma parte de una arquitectura separada por responsabilidades:

```text
osmi-server    → lógica de negocio y persistencia
osmi-gateway   → entrada HTTP, middleware y puente REST/gRPC
osmi-protobuf  → contratos gRPC/HTTP
osmi-db        → migraciones, seeds y esquema PostgreSQL
osmi-front     → aplicación web
```

Cada módulo mantiene su propia documentación y ciclo de cambios.

---

## Principios del proyecto

- Una sola autoridad para cada transición crítica.
- Estados explícitos antes que efectos implícitos.
- Transacciones para mantener consistencia.
- Idempotencia en integraciones externas.
- Guest checkout como caso de negocio de primera clase.
- Separación entre autenticación (`users`) y relación comercial (`customers`).
- Sin secretos en Git.
- Sin rutas alternativas de venta que evadan el pago.
- Cambios de esquema gestionados exclusivamente por `osmi-db`.

---

# Estructura del proyecto
```bash
osmi-server/
├── .github/
│    └── workflows/
│    │  ├── ci.yml         ← pruebas (go test, go vet, etc.)
│    │  ├── docker.yml     ← build + push a GHCR
│    │  └── deploy.yml     ← despliegue automático a EC2
├── cmd/
│   └── worker/
│   │   ├── main.go
│   └── main.go                              # Punto de entrada de la aplicación
├── config/                                  # Archivos de configuración YAML
│   ├── deployment.yaml
│   ├── development.yaml                     # Configuración para entorno de desarrollo
│   ├── production.yaml                      # Configuración para entorno de producción  
│   └── staging.yaml                         # Configuración para entorno de staging
├── internal/                                # Código interno de la aplicación
│   ├── api/                                 # Capa de presentación (HTTP/gRPC)
│   │   ├── dto/                             # Data Transfer Objects
│   │   │   ├── api_call/                    #     
│   │   │   │   ├── filter.go                #
│   │   │   │   ├── request.go               #  
│   │   │   │   ├── response.go              #
│   │   │   ├── audit/                       #     
│   │   │   │   ├── filter.go                #
│   │   │   │   ├── request.go               #  
│   │   │   │   ├── response.go           #
│   │   │   ├── category/                    #     
│   │   │   │   ├── filter.go             #
│   │   │   │   ├── request.go         #  
│   │   │   │   ├── response.go           #
│   │   │   ├── common/                    #     
│   │   │   │   ├── geo_location.go            #
│   │   │   │   ├── health.go         #  
│   │   │   │   ├── map_bounds.go        #
│   │   │   │   ├── meta.go         #  
│   │   │   │   ├── pagination.go      #
│   │   │   ├── country_config/                    #     
│   │   │   │   ├── filter.go             #
│   │   │   │   ├── request.go         #  
│   │   │   │   ├── response.go           #
│   │   │   ├── customer/                    #     
│   │   │   │   ├── filter.go             #
│   │   │   │   ├── request.go         #  
│   │   │   │   ├── response.go           #
│   │   │   ├── event/                    #     
│   │   │   │   ├── filter.go             #
│   │   │   │   ├── request.go         #  
│   │   │   │   ├── response.go           #
│   │   │   ├── invoice/                    #     
│   │   │   │   ├── filter.go             #
│   │   │   │   ├── request.go         #  
│   │   │   │   ├── response.go           #
│   │   │   ├── notification/                    #     
│   │   │   │   ├── filter.go             #
│   │   │   │   ├── request.go         #  
│   │   │   │   ├── response.go           #
│   │   │   ├── order/                    #     
│   │   │   │   ├── filter.go             #
│   │   │   │   ├── request.go         #  
│   │   │   │   ├── response.go           #
│   │   │   ├── organizer/                    #     
│   │   │   │   ├── filter.go             #
│   │   │   │   ├── request.go         #  
│   │   │   │   ├── response.go           #
│   │   │   ├── payment/                    #     
│   │   │   │   ├── filter.go             #
│   │   │   │   ├── request.go         #  
│   │   │   │   ├── response.go           #
│   │   │   ├── refund/                    #     
│   │   │   │   ├── filter.go             #
│   │   │   │   ├── request.go         #  
│   │   │   │   ├── response.go           #
│   │   │   ├── ticket/                    #     
│   │   │   │   ├── filter.go             #
│   │   │   │   ├── request.go         #  
│   │   │   │   ├── response.go           #
│   │   │   ├── ticket_type/                    #     
│   │   │   │   ├── filter.go             #
│   │   │   │   ├── request.go         #  
│   │   │   │   ├── response.go           #
│   │   │   ├── user/                    #     
│   │   │   │   ├── filter.go             #
│   │   │   │   ├── request.go         #  
│   │   │   │   ├── response.go           #
│   │   │   ├── venue/                    #     
│   │   │   │   ├── filter.go             #
│   │   │   │   ├── request.go         #  
│   │   │   │   ├── response.go           #
│   │   │   ├── webhook/                    #     
│   │   │   │   ├── filter.go             #
│   │   │   │   ├── request.go         #  
│   │   │   │   ├── response.go           #
│   │   │   ├── dto.go
│   │   ├── grpc/                            # Servidor y configuración gRPC
│   │   │   ├── interceptors/                # Interceptores/middleware gRPC
│   │   │   │   ├── auth_interceptor.go      # Interceptor de autenticación JWT
│   │   │   │   ├── logging_interceptor.go   # Interceptor de logging de peticiones
│   │   │   │   └── validation_interceptor.go # Interceptor de validación de datos
│   │   │   └── adapter.go                   # 
│   │   │   └── server.go                    # Configuración e inicialización del servidor gRPC
│   │   └── helpers/                         #
│   │   │   └── helpers.go                   # 
│   ├── application/                         # LÓGICA DE NEGOCIO (usa interfaces)
│   │   ├── handlers/                         # Manejadores de peticiones
│   │   │   ├── grpc/                         # Handlers para gRPC
│   │   │   │   ├── category_handler.go        # Handler de categorias (gRPC)
│   │   │   │   ├── customer_handler.go      # Handler de clientes (gRPC)
│   │   │   │   ├── event_handler.go         # Handler de eventos (gRPC)
│   │   │   │   ├── handler.go              # unificado que implementa OsmiServiceServer con todos los métodos.
│   │   │   │   ├── order_handler.go
│   │   │   │   ├── payment_handler.go
│   │   │   │   ├── ticket_handler.go        # Handler de tickets (gRPC)
│   │   │   │   ├── ticket_type_handler.go      # Handler de tipos de tickets (gRPC)
│   │   │   │   └── user_handler.go          # Handler de usuarios (gRPC)
│   │   │   │   └── webhook_handler.go
│   │   │   └── http/                       # Handlers para HTTP REST
│   │   │       ├── event_handler.go         # Handler de eventos (HTTP)
│   │   │       └── ticket_handler.go        # Handler de tickets (HTTP)
│   │   └── services/                       # Servicios de aplicación
│   │       ├── category_service.go          # Servicio de gestión de categorías
│   │       ├── customer_service.go          # Servicio de gestión de clientes
│   │       ├── event_service.go             # Servicio de gestión de eventos
│   │       ├── order_service.go
│   │       ├── payment_service.go 
│   │       ├── services.go
│   │       ├── ticket_service.go            # Servicio de gestión de tickets
│   │       ├── ticket_type_service.go
│   │       └── user_service.go              # Servicio de gestión de usuarios
│   ├── config/                             # Configuración interna de la aplicación
│   │   ├── config.go                       # Configuración principal de la aplicación
│   │   └── environment.go                  # Manejo y validación de variables de entorno
│   ├── context/                             # 
│   │   ├── context.go                       # 
│   ├── database/                           # Acceso y gestión de base de datos
│   │   ├── connection.go                   # Conexión y pool de conexiones a PostgreSQL
│   ├── domain/                             # Dominio del negocio (DDD)
│   │   ├── entities/                      # Entidades de dominio / Entidades de negocio
│   │   │   ├── api_call.go                # Entidad: Llamadas API de integración
│   │   │   ├── audit.go                   # Entidad: Registros de auditoría del sistema
│   │   │   ├── category.go                # Entidad: Categorías de eventos
│   │   │   ├── country_config.go          # Entidad: Configuración fiscal por país
│   │   │   ├── customer.go                # Entidad: Clientes del sistema CRM
│   │   │   ├── event.go                   # Entidad: Eventos del sistema de ticketing
│   │   │   ├── invoice.go                 # Entidad: Facturas del sistema fiscal
│   │   │   ├── notification.go            # Entidad: Notificaciones enviadas a usuarios
│   │   │   ├── notification_template.go   # Entidad: Plantillas de notificación
│   │   │   ├── order.go                   # Entidad: Órdenes de compra del sistema de billing
│   │   │   ├── organizer.go               # Entidad: Organizadores de eventos
│   │   │   ├── payment.go                 # Entidad: Pagos procesados
│   │   │   ├── payment_provider.go        # Entidad: Proveedores de servicios de pago
│   │   │   ├── refund.go                  # Entidad: Reembolsos procesados
│   │   │   ├── session.go                 # Entidad: Sesiones de usuario activas
│   │   │   ├── ticket.go                  # Entidad: Tickets vendidos o reservados
│   │   │   ├── ticket_type.go             # Entidad: Tipos/configuraciones de tickets
│   │   │   ├── user.go                    # Entidad: Usuarios del sistema de autenticación
│   │   │   ├── venue.go                   # Entidad: Lugares o recintos para eventos
│   │   │   └── webhook_stats.go           #
│   │   │   └── webhook.go                 # Entidad: Webhooks configurados para integraciones
│   │   ├── enums/                         # Enumeraciones del dominio
│   │   │   ├── audit_severity.go          # Enum: Niveles de severidad para logs de auditoría
│   │   │   ├── event_status.go            # Enum: Estados posibles de un evento (draft, published, cancelled, etc.)
│   │   │   ├── notification_status.go     # Enum: Estados de notificaciones (pending, sent, failed, etc.)
│   │   │   ├── order_status.go            # Enum: Estados de órdenes (pending, paid, cancelled, refunded, etc.)
│   │   │   ├── payment_status.go          # Enum: Estados de pagos (pending, completed, failed, etc.)
│   │   │   └── ticket_status.go           # Enum: Estados de tickets (available, reserved, sold, checked_in, etc.)
│   │   │   └── user_role.go               # Enum: Estados de usuarios
│   │   ├── events/                        # Eventos de dominio
│   │   │   ├── event_published.go         # Evento de dominio: Evento publicado
│   │   │   └── ticket_purchased.go        # Evento de dominio: Ticket comprado
│   │   ├── repository/                    # Interfaces de repositorio (puertos)
│   │   │   ├── api_call_repository.go     # Interfaz: Repositorio de llamadas API
│   │   │   ├── audit_repository.go        # Interfaz: Repositorio de auditoría
│   │   │   ├── category_repository.go     # Interfaz: Repositorio de categorías
│   │   │   ├── country_config_repository.go # Interfaz: Repositorio de configuración por país
│   │   │   ├── customer_repository.go     # Interfaz: Repositorio de clientes
│   │   │   ├── errors
│   │   │   ├── event_repository.go        # Interfaz: Repositorio de eventos
│   │   │   ├── invoice_repository.go      # Interfaz: Repositorio de facturas
│   │   │   ├── notification_repository.go # Interfaz: Repositorio de notificaciones
│   │   │   ├── notification_template_repository.go # Interfaz: Repositorio de plantillas de notificación
│   │   │   ├── order_repository.go        # Interfaz: Repositorio de órdenes
│   │   │   ├── organizer_repository.go    # Interfaz: Repositorio de organizadores
│   │   │   ├── payment_provider_repository.go # Interfaz: Repositorio de proveedores de pago
│   │   │   ├── payment_repository.go      # Interfaz: Repositorio de pagos
│   │   │   ├── refund_repository.go       # Interfaz: Repositorio de reembolsos
│   │   │   ├── session_repository.go      # Interfaz: Repositorio de sesiones
│   │   │   ├── ticket_repository.go       # Interfaz: Repositorio de tickets
│   │   │   ├── ticket_type_repository.go  # Interfaz: Repositorio de tipos de ticket
│   │   │   ├── user_repository.go         # Interfaz: Repositorio de usuarios
│   │   │   ├── venue_repository.go        # Interfaz: Repositorio de lugares/recintos
│   │   │   └── webhook_repository.go      # Interfaz: Repositorio de webhooks
│   │   └── valueobjects/                  # Objetos de valor (inmutables)
│   │       ├── currency.go                # Objeto valor: Moneda con validación ISO 4217
│   │       ├── email.go                   # Objeto valor: Email validado con estructura correcta
│   │       ├── money.go                   # Objeto valor: Dinero (monto + moneda) para cálculos financieros
│   │       ├── phone.go                   # Objeto valor: Teléfono validado con formato internacional
│   │       └── uuid.go                    # Objeto valor: UUID validado
│   ├── infrastructure/                     # Infraestructura (implementaciones técnicas)
│   │   ├── cache/                         # Sistema de caché distribuido
│   │   │   ├── cache_service.go           # Servicio abstracto de caché
│   │   │   └── redis_client.go            # Implementación con Redis
│   │   ├── email/                         # 
│   │   │   ├── ses_client.go
│   │   ├── messaging/                     # Sistema de mensajería y notificaciones
│   │   │   ├── email_sender.go            # Servicio de envío de emails (SMTP/SendGrid)
│   │   │   └── notification_service.go    # Servicio unificado de notificaciones
│   │   ├── payment/                       # Sistema de procesamiento de pagos
│   │   │   ├── payment_gateway.go         # Interfaz abstracta de gateway de pagos
│   │   │   └── stripe_client.go
│   │   │   └── stripe_service.go          # Implementación con Stripe API
│   │   ├── qr/
│   │   │   ├── qr_generator.go
│   │   └── repositories/                  # Implementaciones de repositorios (adaptadores)
│   │       ├── inmemory/                  # Repositorios en memoria para testing
│   │       └── postgres/                  # Repositorios PostgreSQL (implementaciones reales)
|   |           ├── helpers/
|   |           |    ├── errors/                  # Paquete para errores
|   |           |    │   ├── postgres_errors.go   # Errores PostgreSQL
|   |           |    │   ├── validation_errors.go # Errores validación
|   |           |    │   └── transaction_errors.go # Errores transacciones
|   |           |    ├── query/                   # Paquete para construcción queries
|   |           |    │   ├── builder.go           # Query builder base
|   |           |    │   ├── filters.go           # Construcción filtros
|   |           |    │   └── pagination.go        # Paginación
|   |           |    ├── scanner/                 # Paquete para scanning
|   |           |    │   ├── scanner.go           # Scanner genérico
|   |           |    │   ├── user_scanner.go      # Scanner específico usuarios
|   |           |    │   └── ticket_scanner.go    # Scanner específico tickets
|   |           |    ├── types/                   # Paquete para conversiones
|   |           |    │   ├── types.go             # Conversiones básicas
|   |           |    │   ├── ticket_types.go      # Conversiones específicas tickets
|   |           |    │   └── user_types.go        # Conversiones específicas usuarios
|   |           |    └── utils/                   # Utilidades varias
|   |           |    |    ├── datetime.go          # Funciones fecha/hora
|   |           |    |    ├── strings.go           # Funciones strings
|   |           |    |    └── logging.go           # Logging
|   |           |    ├── validations/             # Paquete validaciones
|   |           |    │   ├── basic_validations.go # Validaciones básicas
|   |           |    │   ├── business_validations.go # Validaciones negocio
|   |           |    │   └── domain_validations.go # Validaciones dominio
│   │           ├── category_repository.go        # Implementación PostgreSQL de repositorio de categorías
│   │           ├── customer_repository.go        # Implementación PostgreSQL de repositorio de clientes
│   │           ├── event_repository.go           # Implementación PostgreSQL de repositorio de eventos
│   │           ├── order_repository.go
│   │           ├── organizer_repository.go       # 
│   │           ├── payment_repository.go
│   │           ├── ticket_repository.go          # Implementación PostgreSQL de repositorio de tickets
│   │           ├── ticket_type_repository.go     # 
│   │           └── user_repository.go            # Implementación PostgreSQL de repositorio de usuarios
│   │           ├── venue_repository.go           # 
│   └── repository/                               # 
│   │   ├── testdb/                               #
│   │   │   ├── CUSTOMERS-STATUS.md               # 
│   │   │   ├── STATUS.md                         # 
│   │   │   ├── test-customers-fixed.sh           # 
│   │   │   ├── test-customers.sh                         # 
│   │   │   ├── testdb.go                         # 
│   └── shared/                            # Utilidades compartidas entre capas
│       ├── errors/                        # Manejo estructurado de errores
│       │   ├── app_error.go               # Error personalizado de aplicación con contexto
│       │   └── error_codes.go             # Códigos de error estandarizados
│       ├── logger/                        # Sistema de logging estructurado
│       │   ├── logger.go                  # Interfaz abstracta de logger
│       │   └── zap_logger.go              # Implementación con Uber Zap logger
│       ├── security/                      # Utilidades de seguridad
│       │   ├── jwt_service.go             # Servicio JWT para autenticación/authorización
│       │   └── password_hasher.go         # Utilidad para hash y verificación de contraseñas (bcrypt)
│       └── validators/                    # Validadores reutilizables
│           ├── age_validator.go
│           └── init.go
│           ├── iso4217_validator.go
│           └── password_validator.go
│           ├── phone_validator.go
│           └── timezone_validator.go
├── k8s/                                   # Configuración Kubernetes (manifests YAML)
    ├── base/                    # Configuraciones base (opcional, si usas Kustomize)
    ├── overlays/
    │   ├── development/        # Config desarrollo
    │   │   ├── deployment.yaml
    │   │   ├── service.yaml
    │   │   └── kustomization.yaml
    │   ├── staging/           # Config staging  
    │   │   ├── deployment.yaml
    │   │   ├── service.yaml
    │   │   └── kustomization.yaml
    │   └── production/        # Config producción
    │       ├── deployment.yaml
    │       ├── service.yaml
    │       └── kustomization.yaml
    └── manifests/             # Manifests crudos (alternativa)
        ├── deployment.yaml
        ├── service.yaml
        ├── configmap.yaml
        └── ingress.yaml
├── tests/                                 # Pruebas automatizadas
│   ├── e2e/                               # Pruebas end-to-end
│   │   ├── checkin_flow_test.go           # Prueba completa del flujo de check-in
│   │   └── purchase_flow_test.go          # Prueba completa del flujo de compra
│   ├── integration/                       # Pruebas de integración
│   │   ├── api_integration_test.go        # Pruebas de integración de API HTTP/gRPC
│   │   ├── database_integration_test.go   # Pruebas de integración con base de datos
│   │   └── payment_integration_test.go    # Pruebas de integración con servicios de pago
│   └── unit/                              # Pruebas unitarias
│       ├── application/                   # Pruebas de la capa de aplicación
│       │   ├── event_service_test.go      # Pruebas unitarias del servicio de eventos
│       │   └── ticket_service_test.go     # Pruebas unitarias del servicio de tickets
│       ├── domain/                        # Pruebas del dominio
│       │   ├── ticket_test.go             # Pruebas unitarias de la entidad Ticket
│       │   └── user_test.go               # Pruebas unitarias de la entidad Usuario
│       └── infrastructure/                # Pruebas de la infraestructura
│           ├── payment/                   # Pruebas del sistema de pagos
│           │   └── stripe_service_test.go # Pruebas unitarias del servicio Stripe
│           └── repositories/              # Pruebas de repositorios
│               ├── category_repository_test.go
│               ├── customer_repository_test.go
│               ├── event_repository_test.go
│               ├── ticket_repository_test.go
│               └── user_repository_test.go
├── .dockerignore                          # Archivos a ignorar en builds Docker
├── .env                                   # Variables de entorno para desarrollo local
├── .env.development                       # Variables de entorno para entorno de desarrollo
├── .env.example                           # Plantilla de ejemplo para variables de entorno
├── .env.locaL                             #
├── .env.production                        # Variables de entorno para entorno de producción
├── .env.staging                           # 
├── .gitignore                             # Archivos a ignorar en control de versiones Git
├── CHANGELOG.md                           # Historial de cambios del proyecto
├── Dockerfile                             # Definición de la imagen Docker
├── fix_imports.sh                         #
├── fix-imports.sh                         #
├── fix-packages.sh                        #
├── go.mod                                 # Definición de módulo Go y dependencias
├── go.sum 
├── LICENSE                                # Licencia del software (MIT, Apache, etc.)
├── main.exe
├── README.md                              # Documentación principal del proyecto
└── test_apis.sh                           #
```
































# OSMI Ticket Access & QR Validation Architecture

## Objetivo

OSMI debe proporcionar un sistema de acceso a eventos que sea:

- seguro;
- resistente a duplicación y reutilización de boletos;
- compatible con ventas guest y usuarios registrados;
- auditable;
- escalable a múltiples eventos, organizadores, puertas y miembros de staff;
- utilizable inicialmente desde la web;
- evolucionable a PWA y posteriormente a una aplicación Android dedicada;
- compatible en el futuro con operación offline controlada.

La arquitectura separa explícitamente:

1. identidad del boleto;
2. autenticidad de la credencial QR;
3. validación del boleto;
4. autorización del staff;
5. consumo del boleto mediante check-in;
6. interfaz de scanner.

---

## 1. Estado actual

Cada ticket de OSMI dispone actualmente de campos relacionados con acceso:

- `public_uuid`
- `code`
- `secret_hash`
- `qr_code_data`
- `status`
- `checked_in_at`
- `checked_in_by`
- `checkin_method`
- `checkin_location`
- `validation_count`
- `last_validated_at`

Actualmente:

- `secret_hash` se genera utilizando un UUID aleatorio.
- A pesar del nombre, actualmente no representa un hash criptográfico.
- `qr_code_data` existe en el modelo pero todavía no constituye la credencial oficial de acceso.
- Los QR enviados por email se generan usando únicamente `ticket.Code`.
- Existe una función `ValidateTicket(code, secretHash)`.
- El check-in actual trabaja mediante `ticket_id` y no consume una credencial QR autenticada.
- El repositorio protege el cambio de estado mediante una actualización condicionada a `status = 'sold'`.

Por lo tanto, las piezas principales existen, pero todavía deben unificarse en un flujo formal de acceso.

---

# 2. Principios del sistema de acceso

## 2.1 Mostrar un QR no consume un boleto

El comprador puede:

- abrir su boleto;
- guardar el QR;
- hacer una captura de pantalla;
- leerlo con la cámara de su teléfono;
- mostrarlo varias veces.

Ninguna de esas operaciones modifica el estado del boleto.

El ticket solamente se consume cuando un cliente autorizado de OSMI ejecuta un check-in contra el backend.

Flujo:

    Cliente abre QR
            ↓
    Cámara / visor normal
            ↓
    No cambia estado
            ↓
    Ticket continúa SOLD

En el evento:

    Scanner autorizado OSMI
            ↓
    Backend valida credencial
            ↓
    Backend valida ticket
            ↓
    Backend valida evento
            ↓
    Backend valida permisos del staff
            ↓
    CHECK-IN
            ↓
    SOLD → CHECKED_IN

---

## 2.2 Validar y hacer check-in son operaciones diferentes

OSMI debe distinguir:

### Validate

Responde a la pregunta:

> ¿Esta credencial representa un ticket legítimo y cuál es su estado?

No consume el boleto.

Ejemplos de resultados:

- VALID
- ALREADY_CHECKED_IN
- CANCELLED
- REFUNDED
- NOT_PAID
- WRONG_EVENT
- INVALID_CREDENTIAL
- NOT_FOUND
- CHECKIN_NOT_OPEN
- CHECKIN_CLOSED

### Check-in

Responde a la pregunta:

> ¿Debe consumirse esta entrada ahora?

El check-in sí modifica el ticket.

Estado normal:

    SOLD → CHECKED_IN

Una validación nunca debe provocar automáticamente un check-in.

---

# 3. Credencial QR oficial OSMI

## 3.1 Un UUID no es autenticación

`ticket.public_uuid` puede identificar un ticket, pero conocer un UUID no demuestra que la credencial haya sido emitida por OSMI.

Por ese motivo el QR no debe consistir únicamente en:

    <ticket_uuid>

ni únicamente en:

    <ticket_code>

---

## 3.2 Formato versionado

La credencial deberá ser versionada desde el comienzo.

Formato conceptual:

    OSMI1.<key-id>.<ticket-public-uuid>.<signature>

Ejemplo conceptual:

    OSMI1.k1.8aeb5b87-648d-45c8-9054-faa1de2edef4.<signature>

`OSMI1` identifica la versión del protocolo.

`key-id` identifica la llave utilizada para firmar la credencial.

`ticket-public-uuid` identifica el boleto.

`signature` demuestra que la credencial fue emitida por OSMI.

El formato versionado permite introducir en el futuro:

    OSMI2...

sin invalidar automáticamente los boletos históricos.

---

# 4. Firma criptográfica

## Demo / operación online

La primera versión utilizará HMAC-SHA256.

Conceptualmente:

    message =
        version
        +
        key_id
        +
        ticket_public_uuid

    signature =
        HMAC-SHA256(
            QR_SIGNING_KEY,
            message
        )

La llave:

    QR_SIGNING_KEY

nunca se incluye en:

- QR;
- frontend;
- aplicación Android;
- API pública;
- base de datos del ticket;
- logs.

Solamente el backend puede firmar y verificar credenciales.

Las comparaciones criptográficas deberán realizarse utilizando comparación constante para evitar ataques de timing.

---

# 5. Rotación de llaves

El protocolo incluye `key-id` para permitir rotación futura.

Ejemplo:

    k1
    k2
    k3

El backend mantiene:

    active signing key
    +
    accepted previous verification keys

Cuando se rota una llave:

    nuevos tickets → k2

mientras:

    boletos antiguos k1 → continúan verificándose

Las llaves históricas pueden retirarse cuando ya no exista ningún boleto válido que dependa de ellas.

No es necesario implementar toda la administración automática de llaves en Demo 1.0, pero el formato debe permitirla desde el inicio.

---

# 6. qr_code_data

`qr_code_data` tendrá una función definida.

Debe representar la credencial QR oficial, por ejemplo:

    OSMI1.k1.<ticket-public-uuid>.<signature>

La imagen QR será solamente una representación visual de ese valor.

No se utilizará:

    GenerateQR(ticket.Code)

sino conceptualmente:

    GenerateQR(ticket.QRCodeData)

La credencial podrá almacenarse o reconstruirse de manera determinista dependiendo de la implementación definitiva.

---

# 7. Ciclo de vida del boleto

## Reserva

    ticket created
        ↓
    RESERVED

La credencial puede existir desde la creación del ticket.

Sin embargo, un boleto RESERVED nunca concede acceso.

## Pago

Stripe confirma el pago:

    RESERVED
        ↓
    SOLD

Solamente los tickets SOLD son candidatos normales para acceso.

## Entrada

Scanner OSMI realiza check-in:

    SOLD
        ↓
    CHECKED_IN

## Estados que no permiten entrada

Entre otros:

    RESERVED
    EXPIRED
    CANCELLED
    REFUNDED

---

# 8. Protección contra reutilización

El QR puede copiarse.

La seguridad no consiste en impedir físicamente una captura de pantalla.

La seguridad consiste en que solamente el primer check-in válido pueda consumir el ticket.

Ejemplo:

    Persona A presenta QR
            ↓
    CHECK-IN
            ↓
    SOLD → CHECKED_IN
            ↓
    ACCESO AUTORIZADO

Posteriormente:

    Persona B presenta copia del mismo QR
            ↓
    backend encuentra CHECKED_IN
            ↓
    ACCESO RECHAZADO

El sistema debe informar que el ticket ya fue utilizado y, cuando corresponda, mostrar información como:

    checked_in_at
    gate/location
    validator

sin exponer información sensible innecesaria.

---

# 9. Concurrencia de scanners

Dos dispositivos pueden escanear el mismo ticket casi simultáneamente.

No es suficiente hacer:

    SELECT ticket
    if sold
        UPDATE ticket

porque ambos scanners podrían leer SOLD antes del UPDATE.

El consumo debe protegerse en PostgreSQL mediante una operación atómica.

Conceptualmente:

    UPDATE ticketing.tickets
    SET
        status = 'checked_in',
        checked_in_at = NOW(),
        ...
    WHERE
        id = ?
        AND status = 'sold'
        AND checked_in_at IS NULL

El scanner que modifica una fila obtiene el acceso.

El otro recibe:

    RowsAffected = 0

y debe interpretar el resultado como:

    ALREADY_CHECKED_IN / NOT_AVAILABLE

Nunca como un segundo acceso válido.

---

# 10. Vinculación con evento

Una credencial auténtica no significa automáticamente que sea válida para cualquier puerta.

El backend deberá comprobar:

    credential valid
        ↓
    ticket exists
        ↓
    ticket belongs to event X
        ↓
    scanner is operating event X
        ↓
    staff is authorized for event X

Un ticket del evento B no puede utilizarse en el evento A aunque la firma sea criptográficamente válida.

---

# 11. Autenticación del staff

No se utilizará una contraseña global como:

    scanner123

Cada operador deberá identificarse individualmente.

El sistema utilizará la autenticación normal de OSMI y permisos específicos de staff.

Ejemplos futuros de roles:

    owner
    organizer
    event_manager
    checkin_staff
    support
    admin

El permiso deberá poder limitarse a un evento.

Ejemplo:

    User: Juan

    Role:
        checkin_staff

    Event:
        Evento Desfragmentado Guadalajara

    Permissions:
        validate_ticket = true
        checkin_ticket = true
        refund_ticket = false
        edit_event = false
        view_financials = false

Esto permite revocar acceso de un trabajador sin cambiar credenciales de otros operadores.

---

# 12. Autorización por evento

La autorización deberá modelar conceptualmente:

    user
        ↓
    event_staff_assignment
        ↓
    event
        ↓
    permissions

La implementación concreta se definirá después de revisar el sistema actual de usuarios, roles y permisos.

La autorización no deberá depender únicamente del frontend.

El backend siempre realizará la comprobación.

---

# 13. API de acceso

La API deberá separar validación y consumo.

Diseño conceptual:

    POST /v1/staff/events/{event_id}/tickets/validate

Input:

    credential

Resultado:

    ticket status
    validity
    event
    ticket type
    attendee information allowed for staff
    previous check-in information when applicable

Y:

    POST /v1/staff/events/{event_id}/tickets/check-in

Input:

    credential
    gate/location
    device metadata when applicable

El segundo endpoint ejecuta el cambio de estado atómico.

No debe existir un endpoint público que permita consumir boletos.

---

# 14. Auditoría

La evolución del sistema debe permitir registrar:

    credential scanned
    ticket
    event
    staff user
    device
    gate
    timestamp
    result

Ejemplos:

    VALID
    CHECKED_IN
    ALREADY_USED
    INVALID_SIGNATURE
    WRONG_EVENT
    CANCELLED
    REFUNDED

Esto permitirá:

- soporte operativo;
- investigación de fraude;
- métricas de acceso;
- resolución de disputas;
- análisis de puertas y tiempos de entrada.

La auditoría deberá diseñarse evitando almacenar innecesariamente secretos o credenciales sensibles completas.

---

# 15. Scanner OSMI — primera versión

No se creará inicialmente otro backend ni otra plataforma independiente.

El scanner será una sección protegida del ecosistema OSMI.

Ejemplo:

    www.myosmi.com/staff/check-in

Flujo:

    Login OSMI
        ↓
    verificar rol/permisos
        ↓
    seleccionar evento autorizado
        ↓
    seleccionar puerta opcional
        ↓
    abrir cámara
        ↓
    escanear QR
        ↓
    validar credencial
        ↓
    mostrar resultado
        ↓
    realizar check-in
        ↓
    feedback inmediato

Resultados visuales esperados:

    ✅ ACCESO AUTORIZADO

    ⚠️ BOLETO YA UTILIZADO

    ❌ BOLETO INVÁLIDO

    ❌ BOLETO CANCELADO

    ❌ BOLETO REEMBOLSADO

    ❌ BOLETO DE OTRO EVENTO

---

# 16. Progressive Web App

Después de estabilizar el scanner web, podrá convertirse en PWA.

Ventajas:

- instalación desde navegador;
- icono en pantalla de inicio;
- modo pantalla completa;
- acceso a cámara;
- actualizaciones centralizadas;
- mismo código del scanner web;
- menor coste de mantenimiento inicial.

La PWA seguirá utilizando exactamente la misma API de acceso.

No se duplicará la lógica de negocio en el cliente.

---

# 17. Aplicación Android

Cuando la operación justifique una aplicación nativa, se construirá un cliente Android dedicado.

Arquitectura:

    Android Scanner App
            ↓
       OSMI Access API
            ↓
      OSMI Backend
            ↓
       PostgreSQL

La aplicación Android NO implementará las reglas definitivas de negocio de forma autónoma.

El backend seguirá siendo la autoridad para:

- autenticación;
- autorización;
- autenticidad del ticket;
- estado del ticket;
- evento;
- check-in;
- protección contra replay.

La aplicación Android se encargará principalmente de:

- autenticación del operador;
- selección de evento;
- selección de gate;
- captura de cámara;
- lectura QR;
- UX de acceso;
- comunicación con API;
- feedback visual/sonoro;
- sincronización cuando exista soporte offline.

---

# 18. Identidad de dispositivo

A futuro podrá registrarse cada dispositivo autorizado.

Ejemplo:

    device_id
    organization_id
    assigned_event
    assigned_gate
    last_seen
    app_version
    revoked_at

Esto permitirá:

- revocar scanners perdidos;
- identificar desde qué dispositivo se realizó cada entrada;
- controlar versiones incompatibles;
- administrar terminales de acceso.

No es requisito para Demo 1.0.

---

# 19. Modo offline futuro

Demo 1.0 requiere conexión al backend.

Esto permite garantizar de forma sencilla:

    una entrada
    =
    un check-in

El modo offline NO debe implementarse como una simple copia local de tickets.

Dos scanners desconectados podrían aceptar simultáneamente la misma credencial.

Una futura implementación offline requerirá diseño específico, posiblemente incluyendo:

- firmas asimétricas;
- manifests de evento;
- claves públicas en dispositivos;
- asignación de puertas/dispositivos;
- almacenamiento local cifrado;
- logs append-only;
- estrategia de sincronización;
- resolución de conflictos;
- límites temporales;
- revocaciones.

La existencia del formato versionado `OSMI1`, `OSMI2`, etc. permite evolucionar a ese modelo.

---

# 20. Evolución criptográfica futura

Demo 1.0:

    HMAC-SHA256
    validación online
    backend como autoridad

Futuro:

    Ed25519 u otro esquema de firma asimétrica
    ↓
    backend conserva private key
    ↓
    scanners reciben public key
    ↓
    validación criptográfica local

Esto facilitaría un modo offline seguro sin distribuir el secreto de firma del servidor.

No se implementará criptografía asimétrica hasta que exista una necesidad operativa real.

---

# 21. Orden de implementación

La implementación del sistema seguirá esta secuencia:

    10.1 QR credential
        ↓
    10.2 Credential verification
        ↓
    10.3 Ticket validation
        ↓
    10.4 Atomic check-in
        ↓
    10.5 Concurrent scanner tests
        ↓
    10.6 Event binding
        ↓
    10.7 Staff authentication
        ↓
    10.8 Event staff authorization
        ↓
    10.9 Scanner web UI
        ↓
    10.10 Real ticket test
        ↓
    PWA
        ↓
    Android application
        ↓
    Optional offline architecture

Cada etapa debe quedar probada antes de construir la siguiente.

---

# 22. Criterio de cierre de Demo 1.0

El sistema de acceso se considera funcional cuando se demuestre de extremo a extremo:

    compra real
        ↓
    Stripe confirma pago
        ↓
    ticket SOLD
        ↓
    QR oficial OSMI generado
        ↓
    cliente recibe / muestra QR
        ↓
    scanner autorizado lo lee
        ↓
    credencial válida
        ↓
    evento correcto
        ↓
    staff autorizado
        ↓
    primer check-in aceptado
        ↓
    ticket CHECKED_IN
        ↓
    segundo check-in rechazado

También debe comprobarse:

- QR manipulado → rechazado;
- UUID inventado → rechazado;
- firma incorrecta → rechazada;
- ticket RESERVED → rechazado;
- ticket CANCELLED → rechazado;
- ticket REFUNDED → rechazado;
- ticket de otro evento → rechazado;
- dos scanners simultáneos → solamente uno obtiene acceso.

---

# 23. Principio arquitectónico

El QR no representa autorización para entrar por sí solo.

Representa una credencial emitida por OSMI.

La autoridad definitiva siempre es el backend y el estado actual almacenado por OSMI.

    QR
     ≠
    entrada

    QR
     ↓
    credencial

    credencial
     +
    estado del ticket
     +
    evento
     +
    permisos staff
     +
    política de acceso
     ↓
    entrada autorizada
    






    
## Autor

**Francisco David Zamora Urrutia** | Fullstack Developer · Systems Architect