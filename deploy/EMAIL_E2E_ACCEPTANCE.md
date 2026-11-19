# Criterios de aceptación del correo transaccional

Estado: las pruebas de producción requieren autorización independiente. No repetir pagos para compensar fallas de correo.

| Capa | Prueba verificable | Criterio |
|---|---|---|
| IAM/IMDS | Desde Server, Gateway y Worker de reservas intentar abrir TCP/80 de IMDS sin leer metadatos; desde Email Worker probar obtención de credenciales sin imprimirlas | Solo Email Worker accede. El bloqueo persiste después de reinicio y recreación. La simulación IAM permite únicamente `ses:SendEmail` del remitente y `sqs:ReceiveMessage`/`sqs:DeleteMessage` de la cola. |
| Compra y Outbox | Una compra controlada con cuenta registrada y buzón externo | Pago completado, un boleto, un job por orden, intento con `ses_message_id` persistido al aceptar SES API, sin otro cobro. |
| Entrega | Consultar el estado agregado del job y el buzón propio | Evento `Send` seguido de `Delivery`; job `delivered`, correo visible con QR renderizado, SPF/DKIM/DMARC PASS y boleto accesible en «Mis boletos». El QR nunca se imprime en logs; el scanner queda fuera de esta prioridad. |
| Reintento | En base aislada, simular falla de envío y recuperar el Worker | Job pasa a `retrying`, conserva un solo job por orden, reintenta con espera y finaliza `delivered` o `exhausted` sin otra venta. Puede haber dos correos si SES aceptó uno antes de un fallo de confirmación en DB: entrega *at least once*. |
| Concurrencia | En base aislada, dos despachadores y eventos duplicados/desordenados | Un claim activo por job; una entrega repetida no duplica estado ni supresión. `MessageId` de un intento no cambia. |
| Rebote y queja | Simulador SES a una dirección controlada, sin boleto ni datos comerciales; evento SNS→SQS | Evento llega una vez o más y se procesa idempotentemente. Rebote permanente/queja de un intento rastreado suprime solo el hash del destinatario; métricas, edad de cola, DLQ y alarmas quedan observables. Las pruebas del simulador sin etiqueta no alteran jobs comerciales. |
| Recuperación | Detener/reanudar Worker en entorno aislado con job pendiente | Lease vencido se reclama o marca `exhausted` según límite; no se pierde la orden ni se crea otro boleto. |
| Privacidad | Inspeccionar nombres de variables del Worker y logs de prueba | Sin claves AWS/SMTP permanentes, correo completo, QR, MIME o respuesta cruda de SES en logs. |

La recepción en Principal no es garantía universal de entregabilidad. Para compras invitadas, el correo es el canal de entrega; «Mis boletos» pertenece a cuentas registradas antes de comprar.
