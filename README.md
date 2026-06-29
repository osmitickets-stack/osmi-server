# Osmi Server
Backend gRPC para la plataforma osmi. Este módulo implementa el núcleo del sistema de boletaje digital utilizando una arquitectura escalable, segura y profesional. Incluye servicios gRPC completos, integración con PostgreSQL, y health checks.
---

# Osmi Core Stack
```
Go → lenguaje principal
gRPC → protocolo de comunicación entre servicios
Protobuf → definición de contratos y mensajes
grpc-gateway → puente REST ↔ gRPC (activo)
PostgreSQL → base de datos relacional (conectado)
Kubernetes → orquestación y despliegue (en proceso)
Docker → contenedorización del servicio
.env + godotenv → gestión de variables de entorno
Health & Readiness Probes → verificación de estado del sistema
```

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
├── scripts/                               # Scripts de automatización y utilidad
│   ├── migrate.sh                         # Script para ejecutar migraciones de base de datos
│   └── seed.sh                            # Script para poblar base de datos con datos iniciales
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

## Ejecución con Docker
```
# Construir imagen
docker build -t osmi-server -f docker/Dockerfile .

# Ejecutar contenedor
docker run -p 50051:50051 osmi-server
```

## Endpoints gRPC disponibles
```bash
protobuf
service OsmiService {
  rpc CreateTicket(TicketRequest) returns (TicketResponse);
  rpc ListTickets(UserLookup) returns (TicketListResponse);
  rpc CreateCustomer(CustomerRequest) returns (CustomerResponse);
  rpc GetCustomer(CustomerLookup) returns (CustomerResponse);
  rpc CreateUser(UserRequest) returns (UserResponse);
  rpc CreateEvent(EventRequest) returns (EventResponse);
  rpc GetEvent(EventLookup) returns (EventResponse);
  rpc ListEvents(Empty) returns (EventListResponse);
}
```

## Estado actual
### Completado
Servidor gRPC completamente funcional en puerto 50051
Todos los métodos del servicio implementados
Conexión a PostgreSQL operativa
Health checks activos en puerto 8081
Repositorios para Customers, Tickets y Events
Script de generación de código protobuf

## En Desarrollo
Kubernetes deployment
Autenticación y autorización
Interceptores gRPC
Métricas y monitoring

## Configuración
### Variables de entorno requeridas en .env:
```bash
DATABASE_URL=postgresql://user:pass@host:port/db
GRPC_PORT=50051
HEALTH_PORT=8081
```

# Autor
### Francisco David Zamora Urrutia Fullstack Developer · Systems Architect · Lyricist