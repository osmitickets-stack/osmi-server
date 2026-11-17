# Entrega revisable del guard IMDS para Email Worker

Estado: **preparado en Git; no instalado en EC2**. Requiere una ventana y autorización explícita antes de copiar archivos, instalar unidades, reiniciar servicios o cambiar IAM.

## Evidencia y límite de seguridad

- La EC2 usa IMDSv2 obligatorio con hop limit 2. Server, Gateway y Worker de reservas pueden alcanzar IMDS por TCP. Los tres comparten las redes Docker `application` y `data`; ninguna de ellas debe quedar exceptuada.
- Docker y `docker.socket` están habilitados. `/usr/sbin/nft` existe; `nftables.service` está deshabilitado. No hay tabla `inet osmi_email_imds`, drop-in de Docker ni red `osmi-email-egress` actualmente.
- `/opt/osmi` contiene Compose, no un checkout Git. La entrega se hará mediante un archivo generado desde un commit fijo y un SHA-256 verificado en EC2. No hacer `git pull` en `/opt/osmi`.
- La excepción usa exclusivamente el nombre de interfaz `br-osmi-email`, definido en `compose.email-worker.yml`. La regla cubre también nuevas redes Docker. Los demás contenedores no tienen privilegios, PID del host ni socket Docker; antes de asociar IAM se vuelve a inspeccionar esto. Un administrador del host, acceso al daemon Docker o un escape de contenedor quedan fuera de esta frontera.

## Artefacto y preflight, sin tocar IAM

1. El candidato corregido `C:\Users\silen\Desktop\Servidor\release-candidates\osmi-email-imds-33d7905.tar.gz` se generó con `git archive` desde `33d7905`. Contiene **solo** `deploy/imds-email-worker.nft`, `deploy/ensure-imds-email-guard.sh`, `deploy/systemd/osmi-email-imds-guard.service` y `deploy/systemd/docker-imds-guard.conf`, más dos entradas de directorio. `tar -tvzf` confirmó que no hay enlaces ni rutas adicionales. SHA-256: `3e88a2ce2fe871f2cf6fca7623c3a2ce68b2ca5dad88fa23e1d153d5463fa7a3`. Copiarlo por el canal SFTP ya usado para respaldos a un directorio privado de `ubuntu`. El traslado requiere autorización por ser una escritura en EC2. El paquete anterior `osmi-email-imds-c0ae22f.tar.gz` quedó validado sintácticamente en un staging privado, pero **obsoleto y prohibido para instalar**: al reiniciar su unidad podía cargar reglas duplicadas.
2. Verificar el SHA-256 y los cuatro nombres antes de extraer. No extraer rutas absolutas, `..`, enlaces ni contenido adicional. Mantener el artefacto final como evidencia de reversión.
3. En EC2 confirmar de nuevo: `osmi-email-egress` ausente, tabla `inet osmi_email_imds` ausente, rutas de destino del guard ausentes, Docker y API saludables, IDs de contenedores registrados, IMDSv2 obligatorio y sin credenciales de rol. Revisar las unidades activas con `systemctl cat docker.service docker.socket`; no modificar `/etc/nftables.conf` ni habilitar `nftables.service`.
4. Validar `nft -c -f` sobre el archivo extraído, `sh -n` sobre el guard idempotente y `systemd-analyze verify` sobre la unidad antes de instalar. La comprobación de sintaxis nft ya pasó una vez en EC2, pero debe repetirse con el artefacto exacto.

## Reversión independiente de SSH

El segundo artefacto local es `C:\Users\silen\Desktop\Servidor\release-candidates\osmi-email-imds-rollback-b4069e7.tar.gz`, generado del commit `b4069e7`. Contiene solo `deploy/rollback-imds-email-guard.sh`, `deploy/systemd/osmi-email-imds-rollback.service` y `deploy/systemd/osmi-email-imds-rollback.timer`, más las entradas de directorio. Su SHA-256 es `4ff1625e997c1261712b9dde1b7cc0c0bdc814ac27edf56533cba68c591d914a`. No se ha copiado a EC2.

El guard **no se instalará** sin preparar antes `rollback-imds-email-guard.sh`, `osmi-email-imds-rollback.service` y `osmi-email-imds-rollback.timer`. El timer se habilita y arranca antes de aplicar la tabla nft. `OnActiveSec=15min` cuenta desde su activación; al estar habilitado, si la instancia reinicia durante la ventana, comienza una nueva ventana al arrancar. Se eligió `OnActiveSec` porque `OnBootSec` podría dispararse de inmediato en una instancia que ya lleva más tiempo encendida. [Comportamiento de systemd.timer](https://www.freedesktop.org/software/systemd/man/latest/systemd.timer.html).

`OnActiveSec` es monotónico: `NextElapseUSecRealtime` puede estar vacío aun con el timer programado. Verificar `ActiveState=active`, `UnitFileState=enabled`, `NextElapseUSecMonotonic` y la columna `LEFT` de `systemctl list-timers --all`; no tratar un valor de tiempo real vacío como fallo. Antes de instalar el guard, dejar disparar una vez el timer con las rutas del guard todavía ausentes, cerrar SSH durante la espera y después comprobar `LastTriggerUSecMonotonic` distinto de cero, `Result=success`, `ExecMainStatus=0`, Docker activo y API sana. Así se prueba la ruta automática real sin quitar reglas en uso. Rearmar el timer inmediatamente antes de activar el guard.

El servicio de reversión corre bajo systemd, no bajo la sesión SSH. Comprueba por IMDSv2, sin imprimir ningún token ni nombre de rol, que la ruta de credenciales siga en 404. Comprueba también que cualquier archivo del guard presente coincide con el SHA-256 versionado. Si hay credenciales, el estado IAM es incierto o algún archivo cambió, **deja el guard colocado** y falla cerrado. Si sigue sin credenciales, retira primero el drop-in de Docker, recarga systemd, detiene la unidad, elimina solo la tabla `inet osmi_email_imds` y los archivos propios, y arranca Docker únicamente si estaba inactivo. No vacía otras tablas ni reinicia un Docker sano.

Antes de aplicar el filtro se verificará `rollback.timer` activo y su próxima ejecución. Si falla cualquier comprobación, se puede ejecutar el servicio de reversión manualmente; si se pierde SSH, el timer lo ejecuta en 15 minutos. Tras verificar la instalación, deshabilitar y detener el timer. Para la prueba autorizada de reinicio se vuelve a habilitar **antes** de reiniciar y se deshabilita solo tras confirmar el arranque. El timer debe estar deshabilitado antes de cualquier cambio IAM. Esta protección exige que systemd y la propia EC2 sigan operativos; no sustituye un plan de recuperación de la instancia desde la consola AWS si el sistema no arranca.

## Comandos de despliegue para una ventana futura

**No ejecutar ahora.** El usuario eligió mantener todo en staging. Estos comandos deben revisarse y autorizarse como una sola operación del guard, sin IAM, CloudFormation, migraciones o reinicio de Docker.

Tras copiar el segundo artefacto por SFTP a `$HOME/osmi-email-imds-rollback-b4069e7.tar.gz`, validar el paquete y dejarlo en un staging privado:

```bash
set -euo pipefail
umask 077
bundle="$HOME/osmi-email-imds-rollback-b4069e7.tar.gz"
sha='4ff1625e997c1261712b9dde1b7cc0c0bdc814ac27edf56533cba68c591d914a'
[ -f "$bundle" ] && [ "$(sha256sum "$bundle" | cut -d' ' -f1)" = "$sha" ]
listing="$(tar -tzf "$bundle")"
expected=$'deploy/\ndeploy/rollback-imds-email-guard.sh\ndeploy/systemd/\ndeploy/systemd/osmi-email-imds-rollback.service\ndeploy/systemd/osmi-email-imds-rollback.timer'
[ "$listing" = "$expected" ]
stage="$HOME/osmi-email-imds-rollback-b4069e7-stage"
[ ! -e "$stage" ]
mkdir -m 700 "$stage"
tar -xzf "$bundle" -C "$stage"
sh -n "$stage/deploy/rollback-imds-email-guard.sh"
systemd-analyze verify "$stage/deploy/systemd/osmi-email-imds-rollback.service" "$stage/deploy/systemd/osmi-email-imds-rollback.timer"
echo ROLLBACK_STAGE_VALIDADO
```

Justo antes de instalar, comprobar los destinos vacíos, Docker/API sanos, tabla OSMI ausente y que IMDS no ofrece un rol. El token de IMDSv2 nunca se imprime; solo se usa para obtener un **código HTTP** de la ruta de nombres de rol:

```bash
set -euo pipefail
for p in /opt/osmi/deploy/imds-email-worker.nft /opt/osmi/deploy/ensure-imds-email-guard.sh /opt/osmi/deploy/rollback-imds-email-guard.sh /etc/systemd/system/osmi-email-imds-guard.service /etc/systemd/system/osmi-email-imds-rollback.service /etc/systemd/system/osmi-email-imds-rollback.timer /etc/systemd/system/docker.service.d/90-osmi-email-imds-guard.conf; do
  { [ ! -e "$p" ] && [ ! -L "$p" ]; } || { echo DESTINO_OCUPADO; exit 1; }
done
if sudo nft list table inet osmi_email_imds >/dev/null 2>&1; then echo TABLA_YA_EXISTE; exit 1; fi
token="$(curl --noproxy '*' -fsS --max-time 3 -X PUT -H 'X-aws-ec2-metadata-token-ttl-seconds: 60' 'http://169.254.169.254/latest/api/token')"
code="$(curl --noproxy '*' -sS --output /dev/null --write-out '%{http_code}' --max-time 3 -H "X-aws-ec2-metadata-token: $token" 'http://169.254.169.254/latest/meta-data/iam/security-credentials/')"
unset token
[ "$code" = 404 ] || { echo IAM_PRECHECK_FALLO; exit 1; }
[ "$(systemctl is-active docker.service)" = active ]
[ "$(docker inspect --format '{{.State.Health.Status}}' osmi-server)" = healthy ]
[ "$(docker inspect --format '{{.State.Health.Status}}' osmi-gateway)" = healthy ]
docker exec osmi-postgres pg_isready -q
[ "$(curl --noproxy '*' -sS --output /dev/null --write-out '%{http_code}' --max-time 15 --resolve api.myosmi.com:443:127.0.0.1 https://api.myosmi.com/v1/events)" = 200 ]
[ "$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' --max-time 15 https://www.myosmi.com/)" = 200 ]
echo GUARD_PREFLIGHT_OK
```

Instalar **primero** la reversión y armar el timer. Si `systemctl list-timers` no muestra una próxima ejecución, detenerse. La ventana de 15 minutos comienza aquí, así que la fase siguiente debe ser inmediata:

```bash
set -euo pipefail
stage="$HOME/osmi-email-imds-rollback-b4069e7-stage"
for pair in \
  "$stage/deploy/rollback-imds-email-guard.sh:/opt/osmi/deploy/rollback-imds-email-guard.sh" \
  "$stage/deploy/systemd/osmi-email-imds-rollback.service:/etc/systemd/system/osmi-email-imds-rollback.service" \
  "$stage/deploy/systemd/osmi-email-imds-rollback.timer:/etc/systemd/system/osmi-email-imds-rollback.timer"; do
  src="${pair%%:*}"; dest="${pair#*:}"
  [ ! -e "$dest" ] && [ ! -L "$dest" ] || { echo DESTINO_OCUPADO; exit 1; }
  sudo cp --no-clobber -- "$src" "$dest"
  [ "$(sha256sum "$src" | cut -d' ' -f1)" = "$(sudo sha256sum "$dest" | cut -d' ' -f1)" ] || { echo COPIA_FALLO; exit 1; }
  sudo chown root:root "$dest"; sudo chmod 0644 "$dest"
done
sudo systemctl daemon-reload
sudo systemctl enable --now osmi-email-imds-rollback.timer
[ "$(systemctl is-active osmi-email-imds-rollback.timer)" = active ]
systemctl list-timers --all osmi-email-imds-rollback.timer
echo ROLLBACK_ARMADO
```

Instalar el guard del paquete previamente validado y activarlo **sin reiniciar Docker**. Si falla, no continuar con el drop-in; el timer hará rollback si se pierde la sesión. También se puede iniciar el servicio de reversión manualmente:

```bash
set -euo pipefail
stage="$HOME/osmi-email-imds-33d7905-stage"
sudo systemctl restart osmi-email-imds-rollback.timer
[ "$(systemctl is-active osmi-email-imds-rollback.timer)" = active ]
next="$(systemctl show --value -p NextElapseUSecMonotonic osmi-email-imds-rollback.timer)"
case "$next" in ""|0|n/a) echo TIMER_SIN_DISPARO; exit 1;; esac
systemctl list-timers --all --no-pager osmi-email-imds-rollback.timer
for pair in \
  "$stage/deploy/imds-email-worker.nft:/opt/osmi/deploy/imds-email-worker.nft" \
  "$stage/deploy/ensure-imds-email-guard.sh:/opt/osmi/deploy/ensure-imds-email-guard.sh" \
  "$stage/deploy/systemd/osmi-email-imds-guard.service:/etc/systemd/system/osmi-email-imds-guard.service"; do
  src="${pair%%:*}"; dest="${pair#*:}"
  [ ! -e "$dest" ] && [ ! -L "$dest" ] || { echo DESTINO_OCUPADO; exit 1; }
  sudo cp --no-clobber -- "$src" "$dest"
  [ "$(sha256sum "$src" | cut -d' ' -f1)" = "$(sudo sha256sum "$dest" | cut -d' ' -f1)" ] || { echo COPIA_FALLO; exit 1; }
  sudo chown root:root "$dest"; sudo chmod 0644 "$dest"
done
sudo systemctl daemon-reload
sudo systemctl start osmi-email-imds-guard.service
[ "$(systemctl is-active osmi-email-imds-guard.service)" = active ]
sudo /bin/sh /opt/osmi/deploy/ensure-imds-email-guard.sh check
echo GUARD_ACTIVO
```

Probar bloqueo TCP de los seis contenedores, salud de servicios y API antes de incorporar el drop-in. Son conexiones a puerto 80, **sin solicitar metadatos ni credenciales**. PostgreSQL y Redis ya estaban bloqueados en la línea base; los otros cuatro deben cambiar de accesible a bloqueado:

```bash
set -euo pipefail
for c in osmi-server osmi-gateway osmi-reservation-worker osmi-redis osmi-nginx; do
  if docker exec "$c" sh -c 'nc -z -w 2 169.254.169.254 80 >/dev/null 2>&1'; then echo "IMDS_EXPOSICION=$c"; exit 1; fi
done
if timeout 4 docker exec osmi-postgres bash -c 'exec 3<>/dev/tcp/169.254.169.254/80' >/dev/null 2>&1; then echo IMDS_EXPOSICION=postgres; exit 1; fi
for c in osmi-server osmi-gateway osmi-reservation-worker osmi-postgres osmi-redis osmi-nginx; do
  [ "$(docker inspect --format '{{.State.Status}}' "$c")" = running ] || { echo "CONTENEDOR_CAIDO=$c"; exit 1; }
done
[ "$(docker inspect --format '{{.State.Health.Status}}' osmi-server)" = healthy ]
[ "$(docker inspect --format '{{.State.Health.Status}}' osmi-gateway)" = healthy ]
docker exec osmi-postgres pg_isready -q
[ "$(curl --noproxy '*' -sS --output /dev/null --write-out '%{http_code}' --max-time 15 --resolve api.myosmi.com:443:127.0.0.1 https://api.myosmi.com/v1/events)" = 200 ]
[ "$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' --max-time 15 https://www.myosmi.com/)" = 200 ]
echo IMDS_Y_SERVICIOS_OK
```

Solo con ese resultado, instalar el drop-in y comprobar las dependencias efectivas. `daemon-reload` relee unidades, no reinicia Docker; no usar `systemctl restart docker`:

```bash
set -euo pipefail
stage="$HOME/osmi-email-imds-33d7905-stage"
[ "$(systemctl is-active osmi-email-imds-rollback.timer)" = active ]
dest=/etc/systemd/system/docker.service.d/90-osmi-email-imds-guard.conf
[ ! -e "$dest" ] && [ ! -L "$dest" ] || { echo DESTINO_OCUPADO; exit 1; }
sudo mkdir -p /etc/systemd/system/docker.service.d
sudo cp --no-clobber -- "$stage/deploy/systemd/docker-imds-guard.conf" "$dest"
[ "$(sha256sum "$stage/deploy/systemd/docker-imds-guard.conf" | cut -d' ' -f1)" = "$(sudo sha256sum "$dest" | cut -d' ' -f1)" ] || { echo COPIA_FALLO; exit 1; }
sudo chown root:root "$dest"; sudo chmod 0644 "$dest"
sudo systemctl daemon-reload
sudo systemd-analyze verify docker.service osmi-email-imds-guard.service
systemctl show docker.service -p Requires | grep -Fq osmi-email-imds-guard.service
systemctl show docker.service -p After | grep -Fq osmi-email-imds-guard.service
systemctl cat docker.service | grep -Fq 'ExecStartPre=/bin/sh /opt/osmi/deploy/ensure-imds-email-guard.sh check'
[ "$(systemctl is-active docker.service)" = active ]
echo DOCKER_ORDEN_GUARD_OK
```

La reversión manual, si cualquier comprobación falla, es `sudo systemctl start osmi-email-imds-rollback.service`; confirmar su resultado con `systemctl show -p Result osmi-email-imds-rollback.service` y volver a comprobar Docker/API. Si el servicio se niega a quitar el filtro por IAM incierto o hashes distintos, **no forzar la eliminación**. Tras todas las comprobaciones positivas, detener y deshabilitar `rollback.timer` y confirmar `disabled/inactive`. Para probar persistencia con un reinicio futuro, rearmar el timer **antes** de ese reinicio; no hacerlo en la instalación inicial.

## Instalación inicial, todavía sin rol IAM

1. Instalar primero el script y las dos unidades de reversión, con dueño root y permisos 0644. Validar las unidades, recargar systemd y activar el timer; comprobar su próximo disparo. Luego instalar el archivo nft y `ensure-imds-email-guard.sh` en `/opt/osmi/deploy/`, y la unidad en `/etc/systemd/system/osmi-email-imds-guard.service`, también con dueño root y permisos 0644. Fallar si alguno ya existe. El script carga la tabla solo si no existe y siempre verifica las reglas, de modo que un reinicio de la unidad no las duplica. Ejecutar `systemctl daemon-reload` y arrancar **solo** `osmi-email-imds-guard.service`; no reiniciar Docker.
2. Confirmar que la tabla y las dos reglas de denegación están presentes. Comprobar por TCP, sin consultar tokens ni credenciales, que Server, Gateway, Worker de reservas y Nginx ya no alcanzan IMDS. `nc` está disponible en esos cuatro y en Redis; PostgreSQL tiene Bash y se prueba con un `timeout` externo. La línea base fue: cuatro contenedores expuestos, PostgreSQL y Redis sin acceso. Comprobar que Docker, API, PostgreSQL, Redis, Nginx y los dos workers conservan salud/estado e IDs, y que el HTTPS público y la API de origen responden.
3. Instalar el drop-in versionado como `/etc/systemd/system/docker.service.d/90-osmi-email-imds-guard.conf`, con dueño root y permisos 0644. Ejecutar `systemctl daemon-reload`. Confirmar que `docker.service` tiene `Requires=` y `After=` hacia el guard y que su `ExecStartPre` usa el modo `check` del script para verificar las reglas. No reiniciar Docker aún.
4. En una ventana autorizada aparte, reiniciar la EC2 **antes de asociar IAM**. Tras el arranque comprobar que el guard está activo, que Docker no arrancaría sin él, que los contenedores existentes siguen sin llegar a IMDS, y que API/servicios se recuperaron. Si falla, conservar la identidad IAM sin asociar y revertir los archivos del guard. Este reinicio es necesario para validar persistencia real; `nft -c` por sí solo no la demuestra.

## IAM y Worker, solo después del guard probado

1. Ejecutar y verificar el change set V3 de feedback y un change set separado para el perfil/rol de Email Worker, ambos tras aprobación. El perfil residual `osmi-qr-backup-once` fue eliminado en IAM; su asociación visible no entrega credenciales. Registrar el ID de asociación y verificar que solo la instancia prevista recibe el perfil nuevo. Nunca poner claves AWS permanentes en Compose o archivos de entorno.
2. Antes de asociar el nuevo perfil, comprobar otra vez que los otros contenedores no alcanzan IMDS. Después, probar credenciales temporales únicamente desde Email Worker sin imprimirlas. Confirmar que la red `osmi-email-egress` tiene la interfaz `br-osmi-email` y **solo** Email Worker conectado. Inspeccionar que ningún servicio obtuvo modo host, privilegios o socket Docker.
3. Validar permisos efectivos: SES `SendEmail` solo con remitente/identidad/configuration set previstos; SQS `ReceiveMessage` y `DeleteMessage` solo sobre la cola de feedback. Probar denegaciones fuera de ese alcance con simulación IAM y llamadas no destructivas. Mantener Server en SMTP durante la transición y no retirar sus credenciales hasta que SES API + Outbox + SQS pasen E2E.

## Reversión separada por fase

- **Antes de asociar IAM:** el timer ejecuta el script de reversión descrito arriba aunque SSH se desconecte. También puede invocarse manualmente. Retira únicamente archivos cuyos SHA coinciden y la tabla `inet osmi_email_imds` tras confirmar que IMDS no entrega credenciales. Preserva el paquete privado de staging. Confirmar Docker/API. No hacer `flush ruleset` ni reiniciar un Docker sano.
- **Después de asociar IAM:** detener Email Worker y desasociar el perfil nuevo; verificar que IMDS deja de entregar credenciales. **Mantener el guard activo** mientras exista cualquier credencial de rol alcanzable. La asociación residual eliminada no es un destino de reversión válido. Los respaldos actuales por SCP/SFTP no dependen de ese rol.
- **Después de activar Server/Outbox:** revertir solo Server a su imagen preservada si es necesario. Mantener migración aditiva 14 y Worker mientras existan jobs por procesar; no ejecutar `down` sobre datos comerciales. Consultar `EMAIL_OUTBOX_RUNBOOK.md`.

Ninguna de estas fases constituye una orden para ejecutar cambios ahora. Cada paso que toque producción requiere la autorización prevista por el usuario y verificaciones de salida antes de avanzar.
