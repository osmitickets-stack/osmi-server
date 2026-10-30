# Punto 10 — pendiente 5: seguridad Gateway → Server

El Server acepta conexiones gRPC únicamente mediante TLS 1.3 con autenticación mutua. Verifica la cadena, vigencia y propósito de cliente del certificado y exige el SAN DNS exacto configurado en `GRPC_TLS_GATEWAY_DNS_NAME` (por defecto `osmi-gateway`). Un certificado válido de otro servicio no basta. No se aceptan CommonName ni comodines como identidad del Gateway.

El Gateway presenta su certificado y verifica la CA, la vigencia y el nombre del Server (`GRPC_TLS_SERVER_NAME`, por defecto `osmi-server`). No existe modo de conexión sin TLS ni una opción para omitir la verificación de certificados.

El transporte autentica al servicio. El Gateway autentica al usuario con un JWT HS256 firmado, no vencido, con `exp` obligatorio y `user_id` UUID canónico distinto de cero. El Server recibe un solo `user-id` canónico desde esa conexión autenticada. La autorización para cada evento continúa en `CheckInAuthorizationService`; tener un certificado de servicio no concede permisos de evento a un usuario.

Se eliminan las identidades, roles y tokens arbitrarios de la metadata saliente. Se reconstruyen `user-id` y `authorization` desde el contexto creado por el middleware de autenticación. Se bloquean los encabezados HTTP de identidad interna. Un error del almacén de revocación configurado rechaza la autenticación; no se degrada silenciosamente a omitir la blacklist. Las peticiones públicas anónimas siguen viajando por mTLS, con identidad de usuario vacía. Si una ruta pública de recursos lleva un Bearer, se valida para conservar consultas como perfil y recursos de usuario; un token inválido se rechaza. Login, refresh, health y webhook usan su mecanismo propio y no convierten un encabezado arbitrario en identidad.

Ambos puntos de arranque usan `NewSecureServer`; el antiguo proxy HTTP embebido queda deshabilitado para evitar una entrada alternativa. La reflexión está deshabilitada por defecto y, al habilitarse expresamente, requiere el mismo transporte autenticado.

## Configuración local completa

Este procedimiento prepara una ejecución local futura. Los servicios que ya están ejecutándose no se modifican al editar el código. No copie estos certificados a producción ni suba claves privadas a Git.

Desde Git Bash:

```bash
cd ~/Desktop/Servidor/osmi-back/osmi-server
bash scripts/create-dev-grpc-certs.sh ../.local/grpc-tls
```

El script requiere OpenSSL, emite una CA local de 30 días y certificados de servicio de 7 días. Se niega a sobrescribir un directorio existente. Los certificados incluyen los SAN y propósitos necesarios; la clave de CA no se utiliza por ningún servicio en ejecución.

En la terminal donde iniciará **Server**, desde su directorio:

```bash
export GRPC_TLS_CA_FILE=../.local/grpc-tls/ca.pem
export GRPC_TLS_CERT_FILE=../.local/grpc-tls/server.pem
export GRPC_TLS_KEY_FILE=../.local/grpc-tls/server.key
export GRPC_TLS_GATEWAY_DNS_NAME=osmi-gateway
export GRPC_ENABLE_REFLECTION=false
go run ./cmd
```

En otra terminal donde iniciará **Gateway**, desde su directorio:

```bash
export GRPC_SERVER_ADDR=localhost:50051
export GRPC_TLS_CA_FILE=../.local/grpc-tls/ca.pem
export GRPC_TLS_CERT_FILE=../.local/grpc-tls/gateway.pem
export GRPC_TLS_KEY_FILE=../.local/grpc-tls/gateway.key
export GRPC_TLS_SERVER_NAME=osmi-server
go run ./cmd
```

Los otros ajustes existentes de base de datos, Redis y JWT siguen siendo necesarios. Las variables exportadas prevalecen sobre `.env`; no sustituya sus archivos completos por los ejemplos. El emisor JWT actual genera HS256, `exp` y un UUID público, así que mantiene su formato. Los tokens malformados, sin expiración o con algoritmos distintos ahora se rechazan.

## Operación y crecimiento

- Mantener gRPC en una red privada: no publicar su puerto al navegador o Internet. El navegador usa únicamente HTTP(S) del Gateway.
- Usar una CA privada gestionada y dedicada al enlace, o una política de emisión que solo conceda la identidad de Gateway al Gateway. Proteger la clave de CA fuera de ambos servicios.
- Emitir una clave distinta por instancia y certificados cortos con el mismo SAN de servicio autorizado. Las réplicas no comparten claves privadas ni necesitan coordinar sesiones en memoria.
- Montar CA pública, certificado y clave de cada servicio como archivos de secretos de solo lectura. El Server no recibe la clave privada del Gateway y viceversa. Nunca poner claves en imágenes, Git, logs o `.env` versionados.
- Para rotar una CA, distribuir primero un bundle con la CA antigua y nueva, reiniciar gradualmente, emitir certificados nuevos y luego retirar la CA antigua y reiniciar gradualmente. Los archivos se leen al arrancar; este cambio no implementa recarga automática ni consulta de CRL/OCSP. Usar certificados cortos y reiniciar las conexiones al revocar una identidad.
- El canal gRPC se reutiliza entre peticiones. Los pings del Gateway (60 segundos) respetan el mínimo del Server (30 segundos). El Server recicla las conexiones aproximadamente cada 15 minutos (con el jitter propio de gRPC y un minuto de gracia), para limitar conexiones antiguas; el cliente vuelve a autenticarse por mTLS.
- La revocación de usuarios utiliza el Redis configurado, compartido entre réplicas. Si Redis está configurado pero no disponible, el Gateway falla al arrancar o responde 503 durante la comprobación de revocación. No sustituye la autorización de evento ni la atomicidad del check-in en PostgreSQL.
- Supervisar fallos TLS, caducidad de certificados y disponibilidad de Redis antes del vencimiento. Los errores al cliente no revelan certificados, tokens ni detalles del almacén.
- Una futura herramienta que necesite gRPC directo debe integrarse expresamente con la política de identidad; el antiguo `osmi-client` sin TLS queda rechazado intencionalmente. No usar el certificado del Gateway como credencial general de herramientas.

La activación en producción exige configurar certificados, montajes y dependencias del protobuf en el proceso de despliegue. Esta tarea no modifica EC2, contenedores en ejecución, archivos `.env` existentes ni infraestructura productiva.

## Pruebas

Con el `go.work` local, desde `osmi-back`:

```bash
go test -count=1 -timeout 120s ./osmi-server/internal/api/grpc/... ./osmi-gateway/internal/grpc ./osmi-gateway/internal/middleware ./osmi-gateway/internal/server
go test ./osmi-server/... ./osmi-gateway/... ./osmi-protobuf/...
go build ./osmi-server/... ./osmi-gateway/... ./osmi-protobuf/...
```

Las pruebas de transporte usan puertos aleatorios en loopback y certificados generados en directorios temporales. No necesitan base de datos, Redis, Stripe ni secretos productivos. Las integraciones de negocio requieren la base aislada definida en los pendientes anteriores.

Referencias: [autenticación de gRPC](https://grpc.io/docs/guides/auth/), [crypto/tls de Go](https://pkg.go.dev/crypto/tls), [validación JWT](https://golang-jwt.github.io/jwt/usage/parse/).
