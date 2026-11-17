# Auditoría IAM y Docker para correo transaccional

Estado: **preparación local; no autoriza cambios en producción**.

## Evidencia de solo lectura (16 de noviembre de 2026)

- La consola EC2 muestra la asociación `osmi-qr-backup-once` en estado `associated`, IMDSv2 obligatorio y hop limit 2.
- IAM `GetInstanceProfile` responde `NoSuchEntity` para ese nombre y la ruta IMDS de roles responde 404. CloudTrail registra `RemoveRoleFromInstanceProfile`, `DeleteInstanceProfile` y `DeleteRole` el 6 de noviembre de 2026. La asociación que EC2 sigue mostrando es residual; hoy no entrega credenciales al Worker.
- `osmi-server`, `osmi-gateway` y `osmi-reservation-worker` están en `osmi-stack_application`, sin modo privilegiado, PID del host ni socket Docker montado. Los tres alcanzan TCP/80 en IMDS. PostgreSQL y Redis están en la red interna `osmi-stack_data`.
- La inspección completa de redes confirmó que Server, Gateway y Worker de reservas también están conectados a `osmi-stack_data`; Nginx usa `application` y `public`. La red `osmi-email-egress` todavía no existe. El filtro debe cubrir cualquier red Docker distinta del puente exclusivo del Email Worker.
- Existe la cadena `DOCKER-USER`; `nft` está disponible e `iptables` usa el backend `nf_tables`. `nftables.service` está instalado pero **disabled/inactive**. La política `imds-email-worker.nft` pasó `nft -c` en EC2, que solo comprueba sintaxis. No hay filtro aplicado ni persistencia configurada.
- `/opt/osmi` contiene Compose, pero no es un checkout Git. Docker usa `/usr/lib/systemd/system/docker.service`; `docker.service` y `docker.socket` están habilitados, no hay drop-ins y `/usr/sbin/nft` existe. La tabla `inet osmi_email_imds` y el drop-in propuesto están ausentes. La entrega debe copiar un artefacto versionado y verificarlo antes de instalarlo; un `git pull` en `/opt/osmi` no sería válido.
- `systemd/osmi-email-imds-guard.service` y `systemd/docker-imds-guard.conf` son borradores versionados para cargar solo la tabla OSMI antes de Docker, sin arrancar `nftables.service` ni vaciar reglas existentes. No se han instalado ni validado con systemd en EC2. El drop-in detiene un arranque de Docker si falta la cadena guardada; antes de instalarlo se debe revisar el orden real de unidades y preparar una ventana de reversión.
- Los respaldos externos se descargan por SCP/SFTP; no se identificó una dependencia de IAM para ese traslado. Esto no sustituye una revisión de todas las políticas y tareas de la instancia.

## Decisión de diseño

El Worker usa un cliente IMDSv2 explícito para obtener credenciales temporales y las comparte entre SES API y SQS. No lee la cadena de credenciales de variables de entorno o archivos compartidos. Su Compose declara únicamente variables de base de datos y correo necesarias, y lo ubica en `osmi-email-egress` más la red interna `data`; el resto de contenedores no se une a `osmi-email-egress`.

Un perfil de instancia **pertenece a toda la EC2**, no a un contenedor. La red Docker y `cap_drop` por sí solos no impiden que los demás servicios usen IMDS: la prueba TCP demuestra la exposición. Antes de asociar un rol se necesita un filtro **persistente, aplicado antes de exponer credenciales**, que deniegue `169.254.169.254` a todas las redes de contenedores excepto `br-osmi-email`. Debe comprobarse después de un reinicio y ante recreación de contenedores. No basta con una regla manual temporal. Tampoco se debe asociar un segundo perfil a la misma instancia: EC2 admite una asociación activa, y el perfil visible requiere una investigación y una reversión propias.

Este filtro limita el acceso de los contenedores actuales sin privilegios. No es una frontera frente a un administrador del host/Docker ni a un escape del contenedor. Si se exige aislamiento de identidad frente a esas amenazas, la alternativa es una tarea separada en ECS Fargate con rol de tarea, red y secretos propios. Esa opción cambia infraestructura y costos y requiere otra autorización.

## Condiciones antes de activar IAM

1. Registrar la asociación residual y los IDs de los servicios antes de cualquier cambio. El perfil anterior fue eliminado y no es un destino de reversión válido. Tras autorización, el plan de reversión debe detener el Email Worker y **desasociar** el perfil nuevo; esto recupera el estado operativo actual sin credenciales y preserva los respaldos SCP/SFTP.
2. Diseñar la instalación persistente de la política nft antes de cambiar la asociación IAM. Revisar el contenido actual de `/etc/nftables.conf` y las unidades systemd para no ejecutar un `flush ruleset` sobre reglas Docker. Probar que Server, Gateway y Worker de reservas **no** alcanzan IMDS y que solo Email Worker sí puede obtener credenciales temporales; repetir después de reinicio. La prueba no debe imprimir tokens.
3. Confirmar que el rol final limita SQS a `ReceiveMessage`/`DeleteMessage` de la cola de feedback y SES a `SendEmail` desde `boletos@myosmi.com` con la identidad y el configuration set previstos. Validar con IAM policy simulation y llamadas controladas.
4. Confirmar que la imagen y Compose del Worker no contienen credenciales AWS/SMTP permanentes y que los IDs y salud de los otros contenedores permanecen iguales.

Referencias: [AWS sobre roles de contenedores y límites de aislamiento](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task-iam-roles.html), [Docker DOCKER-USER](https://docs.docker.com/engine/network/firewall-iptables/), [SES IAM](https://docs.aws.amazon.com/ses/latest/dg/control-user-access.html).
