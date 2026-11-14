# Correo transaccional de boletos

El servidor envía por SMTP después de confirmar la venta. Configure el endpoint
de SES de la misma región que la identidad remitente.

- `SMTP_FROM`: dirección simple de una identidad verificada; para OSMI se usa
  una dirección del dominio autenticado `myosmi.com`.
- `SMTP_REPLY_TO`: buzón atendido para respuestas de compradores. Es opcional.
- `SES_CONFIGURATION_SET`: nombre de un conjunto de SES existente en esa región.
  Es opcional si la identidad tiene un conjunto predeterminado.

Una aceptación SMTP no acredita entrega al buzón. Consulte los eventos de
`Delivery`, `Bounce`, `Complaint` y `Delivery delay` en SES/CloudWatch. No
registre direcciones de compradores, credenciales ni códigos QR en logs.
