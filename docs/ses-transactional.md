# Correo transaccional de boletos

La compra confirmada crea un job en el outbox dentro de la transacción. El
Email Worker lo entrega por SES API con credenciales temporales de IMDSv2; el
Server comercial conserva SMTP hasta completar el despliegue y la prueba real
del reemplazo. Ambos transportes comparten la plantilla MIME (texto, HTML y QR).

- `SMTP_FROM`: dirección simple de una identidad verificada; para OSMI se usa
  una dirección del dominio autenticado `myosmi.com`.
- `SMTP_REPLY_TO`: buzón atendido para respuestas de compradores. Es opcional.
- `SES_CONFIGURATION_SET`: nombre de un conjunto de SES existente en esa región.
  Es obligatorio para el Worker y la correlación de eventos; es opcional para
  el Server SMTP si la identidad tiene un conjunto predeterminado.
- El Worker no recibe `SMTP_USERNAME`, `SMTP_PASSWORD`, claves AWS permanentes
  ni un archivo de entorno completo. Su cliente AWS usa el proveedor IMDSv2
  explícito para SES API y SQS.

Una aceptación de SES API no acredita entrega al buzón. Consulte los eventos de
`Delivery`, `Bounce`, `Complaint` y `Delivery delay` en SES/CloudWatch. No
registre direcciones de compradores, credenciales ni códigos QR en logs.
