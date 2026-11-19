# Identidad IAM del Email Worker en la EC2 actual

Estado: **stack de identidad creado y perfil nuevo asociado a `osmi-api-prod` con autorización**. La confianza, políticas, simulación IAM, guard IMDS, seis contenedores y servicios pasaron. Falta comprobar la obtención de credenciales temporales dentro del Email Worker. Esta guía no autoriza cambios adicionales en producción.

## Dependencias comprobadas

- El guard IMDS persistió tras el reinicio controlado: `GUARD_REBOOT_CHECK_PASS` y `GUARD_REBOOT_PASS_TIMER_DESARMADO`. Los seis contenedores existentes no alcanzaron IMDS; Server, Gateway, PostgreSQL, Redis y Nginx conservaron salud y la API y el sitio respondieron HTTP 200.
- CloudFormation V3 creó 15 recursos (`CREATE_COMPLETE`), incluida la política `osmi-transactional-feedback-consumer`. La suscripción SQS usa entrega raw, el destino SES está habilitado y el correo de alarmas quedó confirmado. Un evento SES identificable sigue pendiente.
- El perfil `osmi-qr-backup-once` fue eliminado en IAM; CloudTrail registró su eliminación. EC2 todavía mostraba una asociación residual y la ruta IMDS de nombres de rol respondió 404 incluso después del reinicio. Los respaldos externos se descargan por SCP/SFTP y el respaldo previo a migración 14 ya se restauró en un entorno aislado.

## Crear identidad sin asociarla

1. Confirmar por lectura que la asociación residual sigue en estado `associated` para `osmi-api-prod`, que `GetInstanceProfile(osmi-qr-backup-once)` sigue en `NoSuchEntity`, que el rol y perfil nuevos no existen y que la política de V3 coincide con su salida de CloudFormation. Detenerse si cambió cualquiera de estos hechos.
2. Crear un change set separado desde `ses-worker-identity.yaml` con `CAPABILITY_NAMED_IAM`; revisar que proponga exactamente un `AWS::IAM::Role` y un `AWS::IAM::InstanceProfile`, sin recurso `AWS::EC2::*` ni asociación. No ejecutarlo hasta obtener autorización específica.
3. Ejecutar ese change set en una etapa autorizada. Confirmar `CREATE_COMPLETE`, rol con confianza solo en `ec2.amazonaws.com`, política administrada V3 adjunta y política inline con únicamente `ses:SendEmail` sobre `myosmi.com`/`osmi-transactional-prod` y `ses:FromAddress=boletos@myosmi.com`. Confirmar perfil con solo ese rol. Simular permisos efectivos: permitir esas acciones sobre los recursos exactos; denegar otro remitente, otra identidad, otra cola y la DLQ. El Worker solo usa `sqs:ReceiveMessage` y `sqs:DeleteMessage`; no necesita `GetQueueAttributes`.

## Sustituir la asociación residual

Esta fase requiere otra autorización. Antes de ella, comprobar de nuevo guard activo, reglas correctas, IAM sin credenciales en el host, bloqueo TCP desde los seis contenedores y salud de servicios. Registrar el ID de la asociación vigente sin publicarlo. Usar `ReplaceIamInstanceProfileAssociation` con ese ID y el perfil nuevo; la API sustituye la asociación de la instancia en ejecución. Si la API rechaza la asociación residual, detenerse: no intentar una desasociación alternativa improvisada. Esperar el estado `associated` del perfil nuevo y comprobar la ruta de **nombres** de rol de IMDSv2 desde el host sin abrir el documento de credenciales. Los seis contenedores existentes deben seguir sin llegar a IMDS.

Antes de iniciar el Email Worker, crear solo la red exclusiva `osmi-email-egress` con puente `br-osmi-email` desde el Compose aprobado. Confirmar que solo Email Worker está unido a ella, que no usa red/PID del host, privilegios ni socket Docker, y que su imagen no contiene credenciales permanentes. Comprobar que el Worker puede obtener credenciales temporales sin imprimirlas. El perfil de instancia es una identidad del host: un administrador del host o Docker puede alcanzar sus credenciales; el guard limita a los contenedores actuales no privilegiados, según el modelo de amenazas aceptado.

## Reversión

- Antes de cambiar la asociación: si falla el stack de identidad, no asociar el perfil y conservar Server en SMTP. Corregir el stack solo en una etapa autorizada.
- Después de cambiar la asociación y antes de desplegar Server: si IAM, IMDS o el aislamiento falla, detener Email Worker si ya existe y **desasociar el perfil nuevo** por su ID de asociación. Esperar que el host vuelva a no ofrecer rol y confirmar salud de API y servicios. No intentar restaurar el perfil antiguo eliminado. Mantener el guard activo mientras cualquier credencial de rol pueda existir.
- No retirar SMTP hasta observar SES API, Outbox, Worker, eventos y entrega real. No tocar Gateway, Worker de reservas, PostgreSQL, Redis, Nginx ni respaldos en esta fase.

Referencias: [perfil eliminado en EC2](https://docs.aws.amazon.com/IAM/latest/UserGuide/troubleshoot_iam-ec2.html), [sustitución de asociación](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_ReplaceIamInstanceProfileAssociation.html), [simulador IAM](https://docs.aws.amazon.com/IAM/latest/APIReference/API_SimulatePrincipalPolicy.html).
