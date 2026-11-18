# Inventario previo al despliegue de correo transaccional

Estado: guard IMDS instalado con autorización limitada; demás etapas pendientes. Ningún paso de este documento autoriza cambios adicionales en producción.

## Evidencia y datos que todavía faltan

- El PR #11 sigue en borrador. Su CI pasó en `1d8bec6`; la imagen de GHCR de ese commit **no está publicada** por la CI del PR. `.github/workflows/docker.yml` publica en `main` y una ejecución manual desde la rama podría actualizar `latest`; no usar ese workflow manualmente para el candidato.
- La imagen actual de Server observada en EC2 era `e56c3586db0aa6e1e16d5f8aa00b6690c1621efd`, saludable. Reconfirmar revisión, ID, salud e IDs de todos los contenedores justo antes de cada etapa.
- La base productiva estaba en migración `13|f` tras el reinicio. El respaldo previo a migración 14 pasó `BACKUP_EC2_OK`, `BACKUP_LOCAL_OK`, `BACKUP_SHA_MATCH` y `RESTORE_ISOLATED_PASS`; las copias de EC2 y Windows se conservan.
- El guard `33d7905` y el rollback `b4069e7` pasaron validación SHA-256, contenido y sintaxis en staging privado. Después se instalaron únicamente sus siete archivos nuevos con autorización específica y hashes comprobados; la tabla `inet osmi_email_imds` y el drop-in están activos. El timer automático se probó con disparo real antes de aplicar el guard, se rearmó para la instalación y quedó deshabilitado tras la verificación final. Los destinos ahora están ocupados por esos archivos aprobados; cualquier cambio futuro exige comparar sus hashes y permisos, nunca sobrescribirlos por rutina.
- La verificación posterior al guard confirmó Docker activo; Server, Gateway, PostgreSQL, Redis y Nginx `running/healthy`; Worker de reservas `running`. Tras el reinicio autorizado, `GUARD_REBOOT_CHECK_PASS` y `GUARD_REBOOT_PASS_TIMER_DESARMADO` confirmaron persistencia del guard, bloqueo TCP de IMDS desde los seis contenedores, API y sitio HTTP 200 y timer deshabilitado. La futura excepción del Email Worker todavía no está probada.

## Matriz de cambios previstos

### Diferencias confirmadas entre producción, GitHub y ramas locales

| Componente | Producción observada | GitHub / local | Diferencia y decisión |
| --- | --- | --- | --- |
| Server | Contenedor saludable con revisión `e56c358`, imagen `sha256:8a99838e...` | `main=3e20681`; PR #11 integró localmente ese `main` en `a62f400` sin cambiar el árbol de archivos | Producción carece de Outbox y SES API. `main` ya integró Outbox y la plantilla V3; el PR agrega SES API/IMDS. No publicar la rama borrador como imagen final sin CI y revisión de dependencias. |
| PostgreSQL / osmi-db | `schema_migrations=13`, `dirty=false` | `main=69a7c24` contiene migración 14; la rama local `00d638c` está detrás de `main` | Aplicar solo migración 14 desde imagen verificada, con respaldo y restauración aislada. No fusionar ni desplegar de nuevo la rama antigua ni copiar migraciones a mano. |
| Frontend | Vercel Production READY de `bdae844`, alias `www.myosmi.com` | Rama limpia `fix/purchase-email-experience=d004bd3`, tres archivos sobre ese `main` | Checkout, confirmación y enlaces del Footer pendientes. Publicar separadamente tras build y E2E, con rollback de Vercel. No copiar a EC2. |
| Guard IMDS | Tabla nft y drop-in activos; servicio del guard activo, timer automático deshabilitado | Paquetes versionados `33d7905` y `b4069e7`; runbook en PR #11 | Persistencia tras reinicio y salud de servicios verificadas. Falta probar la excepción del Email Worker antes de asociar IAM. |
| Email Worker | Ausente | Código y `deploy/compose.email-worker.yml` solo en PR #11 | Crear solo el archivo nuevo tras comparar rutas; iniciar solo servicio nuevo cuando IAM, SQS, migración e imagen final estén listos. |
| Gateway, Worker de reservas, Redis, Nginx | En ejecución; Worker de reservas con cero reinicios | Sin cambios necesarios para esta prioridad | No recrear ni desplegar. Los seis IDs existentes permanecieron iguales tras activar el guard. |

La rama local principal de Server `feat/scanner-events` y el checkout local de Front `feat/scanner-web` tienen cambios sin confirmar ajenos a esta prioridad. No incluirlos en artefactos ni sincronizar esos directorios. Los PR borradores de QR/Gateway quedan fuera del despliegue de correo.

En EC2 ya existen `docker-compose.yml`, `deploy/compose.checkin-prod.yml`, `deploy/compose.checkin-images.yml`, `deploy/compose.ses-release.yml` y `deploy/compose.ses-rollback.yml`; se registraron sus SHA-256 sin leer valores. Conservarlos y crear solo archivos nuevos necesarios, después de comprobar ausencia/diferencia. `deploy/compose.email-worker.yml` está ausente. Los hashes de `imds-email-worker.nft`, `ensure-imds-email-guard.sh` y `rollback-imds-email-guard.sh` coinciden con los archivos versionados instalados. No sobrescribir los overrides SES existentes ni usar una sincronización de carpetas.

El change set V3 de CloudFormation propone 15 recursos sin rol ni perfil; el stack sigue `REVIEW_IN_PROGRESS` y el change set `CREATE_COMPLETE/AVAILABLE`, sin ejecutar. Las consultas de solo lectura encontraron libres los dos nombres SNS, dos SQS, la política IAM, el rol/perfil nuevos y las cuatro alarmas; el destino SES propuesto no apareció. Repetir estas comprobaciones justo antes de ejecutar porque AWS puede cambiar. `ses-worker-identity.yaml` prepara el perfil IAM por separado. El perfil residual `osmi-qr-backup-once` aparece asociado en EC2 pero fue eliminado en IAM; IMDS devolvió 404 antes del guard. El rollback IAM sería desasociar el perfil nuevo y mantener SMTP, nunca restaurar un perfil inexistente.

El respaldo nuevo, su SHA-256, copia externa por SCP/SFTP y restauración aislada ya se comprobaron. Conservar ambas copias y el snapshot cifrado; no repetirlos sin una razón técnica concreta.

| Etapa | Destino | Acción prevista | Condición |
| --- | --- | --- | --- |
| Guard, reversión primero | `/opt/osmi/deploy/rollback-imds-email-guard.sh`, `/etc/systemd/system/osmi-email-imds-rollback.{service,timer}` | Crear tres archivos nuevos desde el paquete `b4069e7` | SHA-256 y lista del paquete verificados en EC2; destinos libres. |
| Guard | `/opt/osmi/deploy/imds-email-worker.nft`, `/opt/osmi/deploy/ensure-imds-email-guard.sh`, `/etc/systemd/system/osmi-email-imds-guard.service` | Crear tres archivos nuevos desde el paquete `33d7905` | Timer de reversión armado; destinos libres; sintaxis y hash comprobados. |
| Persistencia guard | `/etc/systemd/system/docker.service.d/90-osmi-email-imds-guard.conf` | Crear un drop-in nuevo, luego `daemon-reload` | Guard activo, aislamiento TCP y salud verificados; no reiniciar Docker en esta etapa. |
| IAM/SES | CloudFormation V3 y `ses-worker-identity.yaml` | Ejecutar change sets separados más adelante | Guard probado, incluida persistencia tras reinicio autorizado; asociación residual y permisos revisados. Ningún archivo de EC2 se copia. |
| Outbox | `osmidb`, migración 14 mediante imagen `osmi-db-migrate` fijada por digest | Aplicar solo `up 1` | Respaldo externo y restauración aislada comprobados; versión 13 limpia; aprobación separada. |
| Email Worker | `/opt/osmi/deploy/compose.email-worker.yml` y override de imagen/configuración nuevos | Crear archivos mínimos y levantar solo el servicio nuevo | Comparar rutas existentes antes de escribir; imagen final verificada por revisión y digest; IAM, SQS, guard y migración listos. |
| Server | Override nuevo de Server e imagen por digest | Recrear solo `osmi-server` | Comparar overrides ya presentes; preservar imagen y variables actuales para rollback; mantener SMTP durante la transición. |
| Frontend | Proyecto Vercel, despliegue del commit aprobado | Publicación separada | Build y verificación de compra/boletos; no copiar archivos a EC2. |

## Archivos y servicios excluidos

No hacer `git pull` en `/opt/osmi`, sincronizar directorios, ejecutar `docker compose down`, usar `--remove-orphans`, reemplazar `docker-compose.yml`, overrides actuales, `/etc/nftables.conf`, unidades base de Docker, volúmenes o archivos de entorno. No recrear Gateway, Worker de reservas, PostgreSQL, Redis ni Nginx. No retirar SMTP hasta aprobar y verificar SES API, Outbox, SQS y entrega real.

## Puntos de control

1. **Solo lectura:** instantánea de rutas, hashes de archivos de destino existentes, revisión/ID/estado de contenedores, IAM 404, tabla nft ausente, respaldos y estado de base. Resolver cualquier diferencia antes de instalar.
2. **Guard:** copiar y validar el paquete de rollback en staging privado. Solicitar autorización independiente para instalar timer, guard y drop-in. Ejecutar las verificaciones y reversión de `EMAIL_IMDS_GUARD_ROLLOUT.md`.
3. **Persistencia:** autorizar por separado el reinicio de EC2 y comprobar guard/servicios tras arranque, antes de asociar IAM.
4. **Infraestructura, datos y aplicación:** change sets, asociación IAM, migración y cada servicio tienen autorizaciones y rollbacks separados. La imagen final se publica solo mediante una ruta que no actualice `latest` desde una rama borrador, o después de fusionar con autorización y pasar el workflow de `main`.
5. **Cierre:** cumplir los criterios medibles de `EMAIL_E2E_ACCEPTANCE.md`. El PASS local no sustituye las pruebas de SES/SQS y compra en producción.
