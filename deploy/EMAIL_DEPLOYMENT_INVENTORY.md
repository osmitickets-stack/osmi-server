# Inventario previo al despliegue de correo transaccional

Estado: guard IMDS persistente, infraestructura SES/SQS e identidad IAM verificadas; migración 14 aplicada. Pendientes: imagen final, Email Worker, Server, frontend y E2E. Ningún paso de este documento autoriza cambios adicionales en producción.

## Evidencia y datos que todavía faltan

- El PR #11 sigue en borrador. Su CI pasó en `1d8bec6`; la imagen de GHCR de ese commit **no está publicada** por la CI del PR. `.github/workflows/docker.yml` publica en `main` y una ejecución manual desde la rama podría actualizar `latest`; no usar ese workflow manualmente para el candidato.
- La imagen actual de Server observada en EC2 era `e56c3586db0aa6e1e16d5f8aa00b6690c1621efd`, saludable. Reconfirmar revisión, ID, salud e IDs de todos los contenedores justo antes de cada etapa.
- La base productiva pasó de `13|f` a `14|f` mediante `up 1` de la imagen verificada. Se confirmaron las tres tablas y los tres índices nuevos, PostgreSQL/Server/Gateway saludables y API HTTP 200. El respaldo previo pasó `BACKUP_EC2_OK`, `BACKUP_LOCAL_OK`, `BACKUP_SHA_MATCH` y `RESTORE_ISOLATED_PASS`; las copias de EC2 y Windows se conservan.
- El guard `33d7905` y el rollback `b4069e7` pasaron validación SHA-256, contenido y sintaxis en staging privado. Después se instalaron únicamente sus siete archivos nuevos con autorización específica y hashes comprobados; la tabla `inet osmi_email_imds` y el drop-in están activos. El timer automático se probó con disparo real antes de aplicar el guard, se rearmó para la instalación y quedó deshabilitado tras la verificación final. Los destinos ahora están ocupados por esos archivos aprobados; cualquier cambio futuro exige comparar sus hashes y permisos, nunca sobrescribirlos por rutina.
- La verificación posterior al guard confirmó Docker activo; Server, Gateway, PostgreSQL, Redis y Nginx `running/healthy`; Worker de reservas `running`. Tras el reinicio autorizado, `GUARD_REBOOT_CHECK_PASS` y `GUARD_REBOOT_PASS_TIMER_DESARMADO` confirmaron persistencia del guard, bloqueo TCP de IMDS desde los seis contenedores, API y sitio HTTP 200 y timer deshabilitado. La futura excepción del Email Worker todavía no está probada.

## Matriz de cambios previstos

### Diferencias confirmadas entre producción, GitHub y ramas locales

| Componente | Producción observada | GitHub / local | Diferencia y decisión |
| --- | --- | --- | --- |
| Server | Contenedor saludable con revisión `e56c358`, imagen `sha256:8a99838e...` | `main=3e20681`; PR #11 integró localmente ese `main` en `a62f400` sin cambiar el árbol de archivos | Producción carece de Outbox y SES API. `main` ya integró Outbox y la plantilla V3; el PR agrega SES API/IMDS. No publicar la rama borrador como imagen final sin CI y revisión de dependencias. |
| PostgreSQL / osmi-db | `schema_migrations=14`, `dirty=false`; tres tablas y tres índices nuevos verificados | `main=69a7c24` contiene migración 14; la rama local `00d638c` está detrás de `main` | Migración completada mediante imagen verificada. No repetirla, ejecutar `down` ni desplegar de nuevo la rama antigua. |
| Frontend | Vercel Production READY de `bdae844`, alias `www.myosmi.com` | Rama limpia `fix/purchase-email-experience=d004bd3`, tres archivos sobre ese `main` | Checkout, confirmación y enlaces del Footer pendientes. Publicar separadamente tras build y E2E, con rollback de Vercel. No copiar a EC2. |
| Guard IMDS e IAM | Guard activo tras reinicio; perfil nuevo asociado; seis contenedores sin acceso IMDS | Paquetes versionados `33d7905` y `b4069e7`; stack IAM con rol y perfil | `IAM_SIMULATION_PASS`, `IAM_ASSOCIATED_PASS` e `IAM_HOST_VERIFICATION_PASS`. Falta probar obtención de credenciales temporales por el Email Worker. |
| Email Worker | Ausente | Código y `deploy/compose.email-worker.yml` solo en PR #11 | Crear solo el archivo nuevo tras comparar rutas; iniciar solo servicio nuevo cuando IAM, SQS, migración e imagen final estén listos. |
| Gateway, Worker de reservas, Redis, Nginx | En ejecución; Worker de reservas con cero reinicios | Sin cambios necesarios para esta prioridad | No recrear ni desplegar. Los seis IDs existentes permanecieron iguales tras activar el guard. |

La rama local principal de Server `feat/scanner-events` y el checkout local de Front `feat/scanner-web` tienen cambios sin confirmar ajenos a esta prioridad. No incluirlos en artefactos ni sincronizar esos directorios. Los PR borradores de QR/Gateway quedan fuera del despliegue de correo.

En EC2 ya existen `docker-compose.yml`, `deploy/compose.checkin-prod.yml`, `deploy/compose.checkin-images.yml`, `deploy/compose.ses-release.yml` y `deploy/compose.ses-rollback.yml`; se registraron sus SHA-256 sin leer valores. Conservarlos y crear solo archivos nuevos necesarios, después de comprobar ausencia/diferencia. `deploy/compose.email-worker.yml` está ausente. Los hashes de `imds-email-worker.nft`, `ensure-imds-email-guard.sh` y `rollback-imds-email-guard.sh` coinciden con los archivos versionados instalados. No sobrescribir los overrides SES existentes ni usar una sincronización de carpetas.

CloudFormation V3 se ejecutó con autorización y terminó `CREATE_COMPLETE`. Sus 15 recursos, la cola cifrada, DLQ y cuatro alarmas pasaron la verificación estructural; el buzón de operaciones confirmó la nueva suscripción SNS. El destino SES está habilitado y la suscripción SNS→SQS usa entrega raw. Una prueba exploratoria de la cola encontró un mensaje visible, pero dos lecturas controladas no pudieron identificarlo como evento SES; no se borró ni se envió otro mensaje. SES→SNS→SQS todavía requiere una prueba de evento identificable. El stack de `ses-worker-identity.yaml` creó exactamente rol y perfil; la confianza, políticas y simulación IAM pasaron. Se sustituyó una vez la asociación residual por el perfil nuevo. El host ve solo el nombre del rol por IMDSv2; los seis contenedores existentes siguen bloqueados y los servicios sanos. El rollback IAM sería desasociar el perfil nuevo y mantener SMTP, nunca restaurar un perfil inexistente.

El respaldo nuevo, su SHA-256, copia externa por SCP/SFTP y restauración aislada ya se comprobaron. Conservar ambas copias y el snapshot cifrado; no repetirlos sin una razón técnica concreta.

| Etapa | Destino | Acción prevista | Condición |
| --- | --- | --- | --- |
| Guard, reversión primero | `/opt/osmi/deploy/rollback-imds-email-guard.sh`, `/etc/systemd/system/osmi-email-imds-rollback.{service,timer}` | Crear tres archivos nuevos desde el paquete `b4069e7` | SHA-256 y lista del paquete verificados en EC2; destinos libres. |
| Guard | `/opt/osmi/deploy/imds-email-worker.nft`, `/opt/osmi/deploy/ensure-imds-email-guard.sh`, `/etc/systemd/system/osmi-email-imds-guard.service` | Crear tres archivos nuevos desde el paquete `33d7905` | Timer de reversión armado; destinos libres; sintaxis y hash comprobados. |
| Persistencia guard | `/etc/systemd/system/docker.service.d/90-osmi-email-imds-guard.conf` | Crear un drop-in nuevo, luego `daemon-reload` | Guard activo, aislamiento TCP y salud verificados; no reiniciar Docker en esta etapa. |
| IAM/SES | CloudFormation V3 e identidad IAM | Completados con autorizaciones separadas | Guard persistente, aislamiento, permisos y asociación nueva verificados. El Worker aún debe demostrar credenciales temporales. |
| Outbox | `osmidb`, migración 14 mediante imagen `osmi-db-migrate` verificada | Completada con `up 1` | Respaldo externo y restauración aislada comprobados; versión 14 limpia, tablas e índices verificados. |
| Email Worker | `/opt/osmi/deploy/compose.email-worker.yml` y override de imagen/configuración nuevos | Crear archivos mínimos y levantar solo el servicio nuevo | Comparar rutas existentes antes de escribir; imagen final verificada por revisión y digest; IAM, SQS, guard y migración listos. |
| Server | Override nuevo de Server e imagen por digest | Recrear solo `osmi-server` | Comparar overrides ya presentes; preservar imagen y variables actuales para rollback; mantener SMTP durante la transición. |
| Frontend | Proyecto Vercel, despliegue del commit aprobado | Publicación separada | Build y verificación de compra/boletos; no copiar archivos a EC2. |

## Archivos y servicios excluidos

No hacer `git pull` en `/opt/osmi`, sincronizar directorios, ejecutar `docker compose down`, usar `--remove-orphans`, reemplazar `docker-compose.yml`, overrides actuales, `/etc/nftables.conf`, unidades base de Docker, volúmenes o archivos de entorno. No recrear Gateway, Worker de reservas, PostgreSQL, Redis ni Nginx. No retirar SMTP hasta aprobar y verificar SES API, Outbox, SQS y entrega real.

## Puntos de control

1. **Completado:** respaldo y restauración aislada; guard y persistencia tras reinicio; CloudFormation V3; identidad IAM, simulación, asociación y aislamiento; migración 14 limpia.
2. **Siguiente:** revisar CI y publicar la imagen final inmutable. Comparar los archivos mínimos de Compose con producción antes de crearlos; levantar solo Email Worker con autorización y comprobar IMDSv2, SES/SQS y salud.
3. **Después:** actualizar solo Server y frontend con autorizaciones separadas, conservando SMTP hasta la entrega real. No usar `latest`, `docker compose down` ni `--remove-orphans`.
4. **Cierre:** cumplir los criterios medibles de `EMAIL_E2E_ACCEPTANCE.md`. El PASS local no sustituye las pruebas de SES/SQS y compra en producción.
