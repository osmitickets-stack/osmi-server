# Inventario previo al despliegue de correo transaccional

Estado: preparación local. Ningún paso de este documento autoriza cambios en producción.

## Evidencia y datos que todavía faltan

- El PR #11 sigue en borrador. Su CI pasó en `1d8bec6`; la imagen de GHCR de ese commit **no está publicada** por la CI del PR. `.github/workflows/docker.yml` publica en `main` y una ejecución manual desde la rama podría actualizar `latest`; no usar ese workflow manualmente para el candidato.
- La imagen actual de Server observada en EC2 era `e56c3586db0aa6e1e16d5f8aa00b6690c1621efd`, saludable. Reconfirmar revisión, ID, salud e IDs de todos los contenedores justo antes de cada etapa.
- La base productiva estaba en migración `13|f`. Hay un respaldo local privado marcado `BACKUP_LOCAL_OK`, pero antes de migrar se requieren comprobación de hash, copia externa y restauración aislada de ese respaldo concreto.
- El guard `33d7905` se validó en un staging privado de EC2. El paquete de rollback `b4069e7` sigue local. Reconfirmar que las siete rutas de instalación no existen y que la tabla nft de OSMI está ausente. Si alguna ruta existe, detenerse y comparar tipo, dueño, permisos y SHA-256 antes de decidir; no sobrescribir.
- Falta una instantánea actual de solo lectura de los archivos y servicios de EC2. Sin ella no puede afirmarse que el contenido local coincide con la configuración productiva.

## Matriz de cambios previstos

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
