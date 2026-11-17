# Entrega transaccional de boletos por SES

## Estado previo requerido

- La rama de Server y la migración 14 pasaron la suite contra `osmidb_test` sin `SKIP`.
- La imagen de Server y la de migración están publicadas y verificadas por revisión y digest. No usar `latest`.
- El respaldo de `osmidb` tiene SHA-256 comprobado y una restauración aislada validada.
- PostgreSQL comercial está en versión 13 limpia antes de migrar; no hay otra migración en curso.
- El configuration set `osmi-transactional-prod` y la identidad `myosmi.com` permanecen activos en `us-east-1`.
- La plantilla `ses-feedback.yaml` se valida como cambio de CloudFormation antes de ejecutarse. Confirmar la suscripción del buzón de alarmas.
- La plantilla SES/SQS V3 crea `WorkerSqsPolicy` sin adjuntarla y no modifica IAM de la instancia. `ses-worker-identity.yaml` prepara un rol con SQS `ReceiveMessage`/`DeleteMessage` sobre la cola y SES `SendEmail` desde `boletos@myosmi.com`, además de un perfil nuevo **no asociado**. No ejecutar su change set hasta resolver el perfil residual `osmi-qr-backup-once` y autorizar explícitamente el cambio de asociación.
- Bloqueo de seguridad: Server, Gateway y Worker de reservas alcanzan IMDS hoy. Antes de asociar una identidad, instalar y probar un filtro persistente que permita IMDS únicamente desde la red exclusiva `osmi-email-egress`; consultar `EMAIL_IAM_DOCKER_AUDIT.md`. Si no puede demostrarse ese aislamiento bajo el modelo de amenazas acordado, no arrancar el Worker en esta EC2.
- El Worker usa SES API con IMDSv2 y no requiere credenciales SMTP. Sale a SES/SQS por `email_egress` y llega a PostgreSQL por `data`. Al validar Compose, pasar el archivo de entorno solo para interpolar las variables declaradas; el contenedor no hereda el archivo completo. Comprobar únicamente **nombres** de variables, sin imprimir valores: no debe recibir claves AWS ni SMTP permanentes.

## Secuencia de publicación

1. Registrar los IDs y digests actuales de Server, Gateway, Worker de reservas, PostgreSQL, Redis y Nginx. Etiquetar la imagen actual de Server para reversión. No recrear los otros servicios.
2. Aplicar **solo** `000014_ticket_email_outbox.up.sql` mediante la imagen verificada de `osmi-db-migrate`, `up 1`, sin seed ni `force`. Confirmar `schema_migrations=14|false` y tablas de notificaciones presentes.
3. Una vez autorizada y verificada la identidad y el filtro IMDS, validar `docker compose config --quiet` y `--dry-run` con `deploy/compose.email-worker.yml` y los overrides de imágenes por digest. Arrancar solo `email-worker` con `--no-deps --pull never --wait`, sin `--remove-orphans`. Su `--health` debe comprobar despacho y recepción SQS.
4. Actualizar solo `server` con imagen por digest, `SES_CONFIGURATION_SET=osmi-transactional-prod`, el remitente actual `boletos@myosmi.com` y Reply-To supervisado. Usar `--no-deps --pull never --wait`. Comprobar revisión, salud, mTLS montado, API HTTPS origen y que los IDs de los demás servicios no cambiaron.
5. Publicar el frontend cuando pase build. Su checkout ya no consulta ni crea clientes por correo; el servidor liga cuentas autenticadas y deja las compras invitadas sin cuenta. Comprobar los PDF de términos y privacidad por GET directo.
6. Hacer **una** compra de prueba con cuenta registrada y buzón externo autorizado. Confirmar orden completada, boleto en «Mis boletos», un solo job de correo `delivered`, intento con ID SES, email en Principal/Promociones/Spam, SPF/DKIM/DMARC y QR renderizado en el correo. No realizar check-in ni repetir un pago lento.
7. Probar SES mailbox simulator desde la consola con mensaje sin datos comerciales: success, bounce, complaint y suppression-list. Confirmar métricas y tránsito SNS/SQS; esos mensajes no llevan `osmi_attempt_id` y no alteran jobs comerciales. Las transiciones de jobs se prueban en PostgreSQL aislado. Comprobar alarmas de rebote, queja, edad de cola y DLQ.
8. Solo después de observar SES API + Worker + SQS + entrega real, verificar que ningún otro flujo Server usa SMTP y retirar las credenciales SMTP persistentes del entorno comercial con un cambio controlado. Conservar la imagen anterior como reversión durante la ventana acordada; el retiro definitivo de esas credenciales exige una estrategia de reversión que no las reintroduzca.

## Controles de operación y reversión

- Vigilar agregados de `notifications.ticket_email_jobs` por estado y edad. `retrying` reintenta con espera limitada, `exhausted` y `rejected` requieren revisión humana; nunca volver a cobrar para reenviar.
- La entrega por SES API es *at least once*: una caída entre aceptación de SES y confirmación en DB puede producir dos correos del mismo boleto. El `order_id` único evita dos jobs por orden y el QR firmado conserva su validez sin crear boletos nuevos.
- Si Server falla tras actualizarse, volver solo a su imagen anterior y override previo. Conservar el Worker y la migración aditiva 14 mientras haya jobs; **no** ejecutar la migración `down` sobre una base con datos de correo.
- Si Worker falla antes de actualizar Server, dejar Server anterior. Investigar IAM/SQS y los mensajes de la DLQ sin revelar destinatarios ni QR. Si falla después, revertir Server y mantener el Worker para drenar jobs ya creados.
- No modificar Gateway, PostgreSQL, Redis ni Nginx fuera del `up 1` de migración autorizado; no ejecutar el workflow general, seeds o `--remove-orphans`.
