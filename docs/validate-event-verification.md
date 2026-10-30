# Punto 10: verificación del pendiente 4

Fecha: 28 de octubre de 2026 (America/Mexico_City).

## Base encontrada

- Server: rama `main`, commit `dc90ac56fa4d40677cd4ad7d3b37ff01cbd1644e`.
- Gateway: rama `main`, commit `8467ab6a32faafd36164cf5ea6d91217701059c8`.
- Protobuf: rama `main`, commit `86e5a4c25b64b419a8d3ba006086367a036d3d9e`.
- El contrato ya tiene `event_id = 2`; Validate ya autoriza por evento antes de verificar la firma y compara el evento del boleto.
- Existen cambios locales previos de seguridad Gateway/Server. No se han descartado ni incluido en un commit de esta intervención.

## Cambio de esta intervención

Se amplía `TestValidateEventIntegration` para verificar, después de las validaciones sin escritura existentes:

1. Check-in aceptado después de Validate.
2. Persistencia de usuario autorizado, contador único, estado y fecha de acceso.
3. Validate de boleto usado conserva autenticidad y rechaza elegibilidad.
4. Segundo Check-in rechazado y comparación completa de ambas filas para detectar mutaciones.

Se reutiliza el servicio y las fixtures existentes, que verifican la marca de aislamiento antes de escribir.

## Evidencia y límites

- Diff de la prueba revisado; `git diff --check` sin diagnósticos para ese archivo.
- Docker Desktop se inició con autorización explícita del usuario. La consulta limitada al contenedor de pruebas devuelve acceso denegado al motor incluso con elevación; no se ejecutó la integración PostgreSQL ni se consultaron sus datos.
- Pasaron las pruebas de servicios y handlers del servidor, seguridad de credenciales e interceptores; también la ejecución específica `TestValidateEvent` sin base configurada (integración omitida, no aprobada).
- Pasaron las pruebas de middleware y del paquete gRPC cliente del gateway. La prueba HTTP/mTLS falla por permisos de conexión TCP loopback, incluso después de repetirla con elevación. La prueba mTLS del servidor también falló por el mismo bloqueo en su ejecución inicial.
- Las compilaciones `go build ./...` de servidor y gateway terminaron con código 0, con avisos de acceso denegado al escribir metadatos de caché de módulos.
- `go test ./...` de protobuf encontró una ruta de terceros bloqueada por la política de archivos (`secrets`); no se intentó leerla ni levantar la restricción. `go test ./gen/pb` y `go build ./gen/pb` terminaron con código 0; el paquete no contiene pruebas.
- No se leyeron archivos de secretos ni se conectó a producción. No se ejecutaron despliegues, migraciones, commits o pushes. No se modificaron configuraciones del stack ni permisos de Windows.
- Al cierre del 28 de octubre faltaba ejecutar la prueba ampliada en PostgreSQL aislado. El resultado posterior consta abajo.

## Lista fija de seguimiento

| N.º | Pendiente | Estado |
| --- | --- | --- |
| 1 | PostgreSQL aislado y migraciones | Aprobado según traspaso; no repetido en esta sesión |
| 2 | Autorización real de staff y checked_in_by | Aprobado según traspaso; se conserva |
| 3 | Concurrencia real | Aprobado según traspaso; se conserva |
| 4 | Proteger Validate por evento | Validado en PostgreSQL aislado; prueba de regresión en commit `2be0525`; push pendiente |
| 5 | Seguridad Gateway → Server | Validado localmente con mTLS, regresiones y compilaciones; push pendiente |
| 6 | Scanner web de OSMI | Pendiente |
| 7 | E2E integral y regresiones | Pendiente |

## Avance verificado el 29 de octubre de 2026

- El usuario comprobó `osmidb_test`, rol `osmi`, marca `osmi-checkin-isolated-v1` y migración 13 limpia en el contenedor `osmi-checkin-test-postgres-test-1`.
- El usuario ejecutó `TestIntegrationDBGuard`, `TestValidateEventIntegration` y `TestListCheckInEventsIntegration` contra esa base: todos pasaron, incluido acceso anónimo denegado, staff de otro evento denegado, boleto de evento incorrecto, Validate sin consumo, check-in único y ausencia de escrituras posteriores. El pendiente 4 queda validado localmente; el push permanece pendiente.
- El generador `cmd/seed-checkin-demo` compiló. El usuario ejecutó su SQL protegido y confirmó `BEGIN`, dos bloques `DO` y `COMMIT`. Se creó una cuenta ficticia de check-in para un evento ficticio en esa base aislada. No se ha probado aún el login por Gateway ni el scanner completo.
- El usuario ejecutó `TestGatewayMTLSBoundary` en Server: pasó, incluidos rechazo de certificado ausente, ajeno, expirado, propósito incorrecto, nombre erróneo, texto plano y TLS 1.2.
- El usuario ejecutó `TestHTTPIdentityThroughMutualTLS` en Gateway: pasó, incluidos cabeceras falsas, petición anónima, token inválido e identidades concurrentes.
- Las 12 pruebas del scanner pasaron; TypeScript y ESLint del módulo y ruta del scanner terminaron sin errores. Falta probar navegador, login y credencial contra el stack aislado.
- Se detectó que el registro público `/v1/users` aceptaba `role=admin` o `role=staff`. El handler ahora fuerza `customer`; la prueba de regresión específica todavía debe terminar antes de considerar cerrada la seguridad.
- La excepción de autenticación de Gateway también cubría `GET /v1/users` y rutas `/v1/users/{id}`. Se restringió la excepción a `POST /v1/users` y se añadió una prueba para GET, PATCH y DELETE anónimos. Falta obtener resultado de esa prueba.
- `go test` de servicios y handlers pasó con `OSMI_TEST_DATABASE_URL` vacío; esto omite integración PostgreSQL, así que el pendiente 4 sigue abierto.
- Server y Gateway publican Docker al hacer push a `main`; el despliegue de producción se encadena al éxito de ese workflow. Los pushes deberán limitarse a ramas de trabajo después de la validación.

## Seguridad Gateway → Server validada localmente

- `TestGatewayMTLSBoundary` pasó en Server: certificado de Gateway aceptado; ausencia de certificado, otro servicio, CA ajena, certificado expirado, propósito incorrecto, nombre de Server erróneo, texto plano y TLS 1.2 rechazados.
- `TestHTTPIdentityThroughMutualTLS` pasó en Gateway: identidad legítima, cabeceras falsas, acceso anónimo, token inválido e identidades concurrentes.
- Pasaron `TestPublicRegistrationCannotChoosePrivilegedRole` en Server y `TestPublicUserRegistrationDoesNotExposeOtherUserRoutes` en Gateway. El usuario confirmó `go build ./...` en ambos repositorios.
- Commits locales: Server `58fabbe` (registro público) y `065c537` (límite mTLS); Gateway `7647d43` (mTLS, identidad y rutas públicas). No se ha hecho push porque no está descartado que un webhook despliegue desde ramas de trabajo.
- Para el E2E del scanner se creó Redis temporal aislado en `127.0.0.1:16379` y certificados locales de prueba. Los ejecutables de Server y Gateway se compilaron en una carpeta temporal nueva. Falta arrancarlos con `osmidb_test` y ejecutar login, selección de evento, Validate y Check-in desde el navegador.
