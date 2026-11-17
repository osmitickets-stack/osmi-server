# Punto de control: persistencia del guard tras reiniciar EC2

Estado: **preparado; reinicio no autorizado ni ejecutado**. El guard y el drop-in están instalados, el timer de rollback está deshabilitado y la instancia no tiene credenciales de rol utilizables. El respaldo nuevo previo a la migración 14 ya se validó; no repetirlo durante esta ventana.

## Condiciones antes de solicitar el reinicio

1. Registrar la revisión, ID y salud de Server y los IDs/estados de Gateway, Worker de reservas, PostgreSQL, Redis y Nginx. Confirmar API de origen y sitio público HTTP 200, `pg_isready` y cero reinicios del Worker de reservas. Confirmar `schema_migrations=13|false`; no aplicar migración 14 durante esta ventana.
2. Confirmar `osmi-email-imds-guard.service=active`, `docker.service=active`, `osmi-email-imds-rollback.timer=disabled/inactive`, `ensure-imds-email-guard.sh check` PASS y que las reglas nft solo pertenecen a `inet osmi_email_imds`. Confirmar IMDSv2 obligatorio, IAM sin credenciales (ruta de nombres de rol HTTP 404) y que los seis contenedores no alcanzan IMDS por TCP. No imprimir tokens ni nombres de rol.
3. Comprobar los hashes de los siete archivos instalados contra los paquetes `33d7905` y `b4069e7`; detenerse ante cualquier diferencia. Conservar ambos paquetes privados de staging. No sobrescribir Compose, unidades base ni `/etc/nftables.conf`.
4. Respaldo previo completado: `BACKUP_EC2_OK`, `BACKUP_LOCAL_OK`, `BACKUP_SHA_MATCH` y `RESTORE_ISOLATED_PASS`. El archivo nuevo en formato custom se verificó con SHA-256 y `pg_restore -l`; la copia externa tuvo el mismo SHA-256 y se restauró por completo en Docker Desktop, en una base nueva dentro de un contenedor sin red ni volúmenes del host. Se verificó `schema_migrations=13|false` y la presencia de tablas. Conservar ambas copias; no restaurar sobre `osmidb` ni repetir el respaldo sin un motivo técnico concreto.
5. El preflight de EC2 confirmó raíz `ebs` e instancia `running`; el preflight de guard, Docker, aplicación, base e IAM pasó. La consola EC2 queda como vía de control del reinicio. No existe evidencia de un snapshot nuevo del volumen raíz: el respaldo PostgreSQL protege los datos de la base, pero no la configuración del host. El timer depende de que EC2 y systemd arranquen; no recupera un host que no inicia. Cualquier snapshot y el reinicio requieren autorización separada.

## Ventana de reinicio, solo tras autorización

1. Repetir preflight de servicios, IAM 404 y reglas. Habilitar y arrancar **antes** del reinicio `osmi-email-imds-rollback.timer`. Verificar `active/enabled`, `NextElapseUSecMonotonic` no vacío y `LEFT` próximo a 15 minutos. `NextElapseUSecRealtime` vacío es normal para `OnActiveSec`.
2. Reiniciar **solo esta EC2** desde la consola EC2 mediante *Reboot instance*, no *Stop/Start*. Confirmar antes que el timer esté armado y que la instancia seleccionada sea la auditada. No ejecutar `docker compose up`, migraciones, cambios IAM ni CloudFormation en esta ventana.
3. Tras reconectar, comprobar: guard activo y reglas exactas, Docker activo, seis contenedores en ejecución, salud de los cinco con healthcheck, Worker de reservas sin reinicios inesperados, PostgreSQL listo, API de origen/sitio público HTTP 200 y bloqueo TCP de IMDS desde los seis contenedores. Confirmar que el timer sigue armado mientras se hacen las pruebas y que sus reglas cargaron antes del arranque de Docker. Registrar PASS/FAIL sin identificadores comerciales ni secretos.
4. Si falla una comprobación y SSH funciona, iniciar `osmi-email-imds-rollback.service` y verificar su `Result`, Docker y API; **no forzar** la retirada si IAM dejó de responder 404 o cambió un hash. Si SSH no funciona pero systemd sí, esperar el disparo del timer, reconectar y verificar el resultado. Si la instancia no arranca, usar el procedimiento de recuperación del volumen/snapshot aprobado; el timer no puede funcionar en ese caso.
5. Solo tras todos los PASS, detener y deshabilitar el timer, comprobar `inactive/disabled`, y conservar el servicio de rollback para reversión manual. No asociar IAM hasta haber registrado el resultado del reinicio.

## Límite de la prueba

La prueba demuestra persistencia para **este** arranque y las redes actuales. Antes de asociar el perfil IAM se debe comprobar de nuevo que solo la futura red `br-osmi-email` puede llegar a IMDS y que ningún contenedor adicional obtuvo privilegios, red host o socket Docker. El perfil EC2 sigue siendo una identidad de toda la instancia, no una identidad por contenedor.
