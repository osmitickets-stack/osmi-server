# Punto de control: persistencia del guard tras reiniciar EC2

Estado: **preparado; reinicio no autorizado ni ejecutado**. Este documento no autoriza crear respaldos, snapshots, reiniciar EC2 ni cambiar IAM. El guard y el drop-in están instalados, el timer de rollback está deshabilitado y la instancia no tiene credenciales de rol utilizables.

## Condiciones antes de solicitar el reinicio

1. Registrar la revisión, ID y salud de Server y los IDs/estados de Gateway, Worker de reservas, PostgreSQL, Redis y Nginx. Confirmar API de origen y sitio público HTTP 200, `pg_isready` y cero reinicios del Worker de reservas. Confirmar `schema_migrations=13|false`; no aplicar migración 14 durante esta ventana.
2. Confirmar `osmi-email-imds-guard.service=active`, `docker.service=active`, `osmi-email-imds-rollback.timer=disabled/inactive`, `ensure-imds-email-guard.sh check` PASS y que las reglas nft solo pertenecen a `inet osmi_email_imds`. Confirmar IMDSv2 obligatorio, IAM sin credenciales (ruta de nombres de rol HTTP 404) y que los seis contenedores no alcanzan IMDS por TCP. No imprimir tokens ni nombres de rol.
3. Comprobar los hashes de los siete archivos instalados contra los paquetes `33d7905` y `b4069e7`; detenerse ante cualquier diferencia. Conservar ambos paquetes privados de staging. No sobrescribir Compose, unidades base ni `/etc/nftables.conf`.
4. Antes del reinicio autorizado, obtener un **respaldo PostgreSQL nuevo** en formato custom bajo un nombre único, verificar `pg_restore -l` y SHA-256, copiarlo por SCP/SFTP fuera de EC2 y comprobar allí el mismo hash. Restaurarlo en una base **aislada** y verificar versión y marcador de aislamiento antes de considerar el respaldo utilizable. No ejecutar `pg_restore` sobre `osmidb`. El respaldo anterior `BACKUP_LOCAL_OK` no prueba por sí solo que la copia externa actual esté íntegra.
5. Preparar recuperación del host: registrar volumen raíz y método de acceso a EC2 desde consola AWS; si se aprueba un snapshot EBS, crear/verificarlo antes del reinicio y registrar su ID fuera del repositorio. El timer depende de que EC2 y systemd arranquen: no recupera un host que no inicia. La creación de snapshot y el reinicio son operaciones productivas con autorización separada.

## Ventana de reinicio, solo tras autorización

1. Repetir preflight de servicios, IAM 404 y reglas. Habilitar y arrancar **antes** del reinicio `osmi-email-imds-rollback.timer`. Verificar `active/enabled`, `NextElapseUSecMonotonic` no vacío y `LEFT` próximo a 15 minutos. `NextElapseUSecRealtime` vacío es normal para `OnActiveSec`.
2. Reiniciar **solo EC2** con el método aprobado. No ejecutar `docker compose up`, migraciones, cambios IAM ni CloudFormation en esta ventana.
3. Tras reconectar, comprobar: guard activo y reglas exactas, Docker activo, seis contenedores en ejecución, salud de los cinco con healthcheck, Worker de reservas sin reinicios inesperados, PostgreSQL listo, API de origen/sitio público HTTP 200 y bloqueo TCP de IMDS desde los seis contenedores. Confirmar que el timer sigue armado mientras se hacen las pruebas y que sus reglas cargaron antes del arranque de Docker. Registrar PASS/FAIL sin identificadores comerciales ni secretos.
4. Si falla una comprobación y SSH funciona, iniciar `osmi-email-imds-rollback.service` y verificar su `Result`, Docker y API; **no forzar** la retirada si IAM dejó de responder 404 o cambió un hash. Si SSH no funciona pero systemd sí, esperar el disparo del timer, reconectar y verificar el resultado. Si la instancia no arranca, usar el procedimiento de recuperación del volumen/snapshot aprobado; el timer no puede funcionar en ese caso.
5. Solo tras todos los PASS, detener y deshabilitar el timer, comprobar `inactive/disabled`, y conservar el servicio de rollback para reversión manual. No asociar IAM hasta haber registrado el resultado del reinicio.

## Límite de la prueba

La prueba demuestra persistencia para **este** arranque y las redes actuales. Antes de asociar el perfil IAM se debe comprobar de nuevo que solo la futura red `br-osmi-email` puede llegar a IMDS y que ningún contenedor adicional obtuvo privilegios, red host o socket Docker. El perfil EC2 sigue siendo una identidad de toda la instancia, no una identidad por contenedor.
