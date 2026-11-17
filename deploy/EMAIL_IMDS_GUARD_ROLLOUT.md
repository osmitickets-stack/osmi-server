# Entrega revisable del guard IMDS para Email Worker

Estado: **preparado en Git; no instalado en EC2**. Requiere una ventana y autorización explícita antes de copiar archivos, instalar unidades, reiniciar servicios o cambiar IAM.

## Evidencia y límite de seguridad

- La EC2 usa IMDSv2 obligatorio con hop limit 2. Server, Gateway y Worker de reservas pueden alcanzar IMDS por TCP. Los tres comparten las redes Docker `application` y `data`; ninguna de ellas debe quedar exceptuada.
- Docker y `docker.socket` están habilitados. `/usr/sbin/nft` existe; `nftables.service` está deshabilitado. No hay tabla `inet osmi_email_imds`, drop-in de Docker ni red `osmi-email-egress` actualmente.
- `/opt/osmi` contiene Compose, no un checkout Git. La entrega se hará mediante un archivo generado desde un commit fijo y un SHA-256 verificado en EC2. No hacer `git pull` en `/opt/osmi`.
- La excepción usa exclusivamente el nombre de interfaz `br-osmi-email`, definido en `compose.email-worker.yml`. La regla cubre también nuevas redes Docker. Los demás contenedores no tienen privilegios, PID del host ni socket Docker; antes de asociar IAM se vuelve a inspeccionar esto. Un administrador del host, acceso al daemon Docker o un escape de contenedor quedan fuera de esta frontera.

## Artefacto y preflight, sin tocar IAM

1. Generar un `tar.gz` desde el commit final del PR #11 que contenga **solo** `deploy/imds-email-worker.nft`, `deploy/ensure-imds-email-guard.sh`, `deploy/systemd/osmi-email-imds-guard.service` y `deploy/systemd/docker-imds-guard.conf`. Registrar commit, lista de cuatro archivos y SHA-256. Copiarlo por el canal SFTP ya usado para respaldos a un directorio privado de `ubuntu`. El traslado también requiere autorización por ser una escritura en EC2. El paquete anterior `osmi-email-imds-c0ae22f.tar.gz` quedó validado sintácticamente en un staging privado, pero **obsoleto y prohibido para instalar**: al reiniciar su unidad podía cargar reglas duplicadas.
2. Verificar el SHA-256 y los cuatro nombres antes de extraer. No extraer rutas absolutas, `..`, enlaces ni contenido adicional. Mantener el artefacto final como evidencia de reversión.
3. En EC2 confirmar de nuevo: `osmi-email-egress` ausente, tabla `inet osmi_email_imds` ausente, rutas de destino del guard ausentes, Docker y API saludables, IDs de contenedores registrados, IMDSv2 obligatorio y sin credenciales de rol. Revisar las unidades activas con `systemctl cat docker.service docker.socket`; no modificar `/etc/nftables.conf` ni habilitar `nftables.service`.
4. Validar `nft -c -f` sobre el archivo extraído, `sh -n` sobre el guard idempotente y `systemd-analyze verify` sobre la unidad antes de instalar. La comprobación de sintaxis nft ya pasó una vez en EC2, pero debe repetirse con el artefacto exacto.

## Instalación inicial, todavía sin rol IAM

1. Instalar el archivo nft y `ensure-imds-email-guard.sh` en `/opt/osmi/deploy/`, y la unidad en `/etc/systemd/system/osmi-email-imds-guard.service`, con dueño root y permisos 0644. Fallar si alguno ya existe. El script carga la tabla solo si no existe y siempre verifica las reglas, de modo que un reinicio de la unidad no las duplica. Ejecutar `systemctl daemon-reload` y arrancar **solo** `osmi-email-imds-guard.service`; no reiniciar Docker.
2. Confirmar que la tabla y las dos reglas de denegación están presentes. Comprobar por TCP, sin consultar tokens ni credenciales, que Server, Gateway y Worker de reservas ya no alcanzan IMDS. Antes de cada prueba confirmar que el contenedor dispone de `nc`, para no confundir ausencia de la herramienta con bloqueo efectivo. Comprobar que Docker, API y demás contenedores conservan salud e IDs.
3. Instalar el drop-in versionado como `/etc/systemd/system/docker.service.d/90-osmi-email-imds-guard.conf`, con dueño root y permisos 0644. Ejecutar `systemctl daemon-reload`. Confirmar que `docker.service` tiene `Requires=` y `After=` hacia el guard y que su `ExecStartPre` usa el modo `check` del script para verificar las reglas. No reiniciar Docker aún.
4. En una ventana autorizada aparte, reiniciar la EC2 **antes de asociar IAM**. Tras el arranque comprobar que el guard está activo, que Docker no arrancaría sin él, que los contenedores existentes siguen sin llegar a IMDS, y que API/servicios se recuperaron. Si falla, conservar la identidad IAM sin asociar y revertir los archivos del guard. Este reinicio es necesario para validar persistencia real; `nft -c` por sí solo no la demuestra.

## IAM y Worker, solo después del guard probado

1. Ejecutar y verificar el change set V3 de feedback y un change set separado para el perfil/rol de Email Worker, ambos tras aprobación. El perfil residual `osmi-qr-backup-once` fue eliminado en IAM; su asociación visible no entrega credenciales. Registrar el ID de asociación y verificar que solo la instancia prevista recibe el perfil nuevo. Nunca poner claves AWS permanentes en Compose o archivos de entorno.
2. Antes de asociar el nuevo perfil, comprobar otra vez que los otros contenedores no alcanzan IMDS. Después, probar credenciales temporales únicamente desde Email Worker sin imprimirlas. Confirmar que la red `osmi-email-egress` tiene la interfaz `br-osmi-email` y **solo** Email Worker conectado. Inspeccionar que ningún servicio obtuvo modo host, privilegios o socket Docker.
3. Validar permisos efectivos: SES `SendEmail` solo con remitente/identidad/configuration set previstos; SQS `ReceiveMessage` y `DeleteMessage` solo sobre la cola de feedback. Probar denegaciones fuera de ese alcance con simulación IAM y llamadas no destructivas. Mantener Server en SMTP durante la transición y no retirar sus credenciales hasta que SES API + Outbox + SQS pasen E2E.

## Reversión separada por fase

- **Antes de asociar IAM:** si el guard afecta salud o red, retirar primero el drop-in de Docker y recargar systemd; detener la unidad, eliminar **solo** la tabla `inet osmi_email_imds` y retirar los dos archivos instalados, preservando copias en el directorio privado de staging. Confirmar Docker/API y que IMDS sigue sin entregar credenciales de rol. No hacer `flush ruleset` ni reiniciar Docker para revertir la instalación inicial.
- **Después de asociar IAM:** detener Email Worker y desasociar el perfil nuevo; verificar que IMDS deja de entregar credenciales. **Mantener el guard activo** mientras exista cualquier credencial de rol alcanzable. La asociación residual eliminada no es un destino de reversión válido. Los respaldos actuales por SCP/SFTP no dependen de ese rol.
- **Después de activar Server/Outbox:** revertir solo Server a su imagen preservada si es necesario. Mantener migración aditiva 14 y Worker mientras existan jobs por procesar; no ejecutar `down` sobre datos comerciales. Consultar `EMAIL_OUTBOX_RUNBOOK.md`.

Ninguna de estas fases constituye una orden para ejecutar cambios ahora. Cada paso que toque producción requiere la autorización prevista por el usuario y verificaciones de salida antes de avanzar.
