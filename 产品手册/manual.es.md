# LangCross · Guía del usuario

> Público objetivo: usuarios del plan personal y todos los miembros de un tenant empresarial/de equipo (usuario normal, administrador de departamento, administrador de la organización)
> Alcance del manual: solo cubre las funciones visibles y disponibles dentro del tenant; no incluye funciones de operación de la plataforma, ni planes, recargas, facturas ni actividades promocionales.
> Los nombres de la interfaz siguen los textos reales del producto; si difieren ligeramente de este documento, prevalece la interfaz.

---

## Índice

1. [Descripción general del producto](#1-descripción-general-del-producto)
2. [Primeros pasos](#2-primeros-pasos)
3. [Roles y permisos](#3-roles-y-permisos)
4. [Traducción instantánea (espacio de trabajo)](#4-traducción-instantánea-espacio-de-trabajo)
5. [Trabajos de traducción (traducción de documentos)](#5-trabajos-de-traducción-traducción-de-documentos)
6. [Editor comparado](#6-editor-comparado)
7. [Base de conocimiento y memoria de traducción](#7-base-de-conocimiento-y-memoria-de-traducción)
8. [Cuenta y configuración personal](#8-cuenta-y-configuración-personal)
9. [Gestión de organización y miembros (administrador de la organización)](#9-gestión-de-organización-y-miembros-administrador-de-la-organización)
10. [Llamadas externas e integraciones (administrador de la organización)](#10-llamadas-externas-e-integraciones-administrador-de-la-organización)
11. [Notificaciones, comentarios y Asistente de IA](#11-notificaciones-comentarios-y-asistente-de-ia)
12. [Preguntas frecuentes](#12-preguntas-frecuentes)
13. [Anexo](#13-anexo)

---

## 1. Descripción general del producto

**LangCross（能言）** es una plataforma de colaboración en traducción mediante IA para empresas y equipos, que integra tres modalidades de traducción y un sistema de activos lingüísticos empresariales:

| Capacidad | Descripción |
|------|------|
| Traducción instantánea | Traducción de texto en estilo de chat: se traduce al escribir, con soporte para traducir a varios idiomas de destino a la vez |
| Trabajos de traducción | Sube archivos docx / pptx / xlsx / pdf, etc. y recorre el flujo completo «crear trabajo → aprobación → traducción → entrega», con archivos de traducción entregados en el formato original |
| Editor comparado | Comparación original/traducción en dos columnas, párrafo a párrafo; permite modificar, aprobar o rechazar con anotaciones |
| Base de conocimiento / memoria de traducción (KB/TM) | Los términos empresariales, los pares bilingües y los nombres de marca con traducción fija se integran en la base de conocimiento y se aplican automáticamente al traducir para unificar los criterios |

Principales características:

- **Más de 40 idiomas** en traducción mutua; la interfaz misma está disponible en 12 idiomas (简体中文、繁體中文、English、日本語、한국어、Deutsch、Français、Español、Português、Русский、العربية、ไทย); la interfaz en árabe usa maquetación de derecha a izquierda.
- **Dos modos de traducción**: «Rápida», ligera y eficiente; «Profesional», con calibración terminológica de la base de conocimiento, evaluación de calidad y controles de barrera estrictos, adecuada para contenido de publicación externa.
- **Aislamiento empresarial**: los datos, las bases de conocimiento y los miembros de cada tenant están aislados entre sí; dentro de la organización, además, las bases de conocimiento y los presupuestos se aíslan por departamento.
- **Coherencia en todos los entornos**: además de la interfaz web, ofrece extensión de selección para navegador, complemento de Word, plugin de VS Code y API/SDK abiertos, con los mismos criterios de traducción que la versión web.

---

## 2. Primeros pasos

### 2.1 Iniciar sesión

Abre la dirección de acceso que te asignó tu empresa (la URL de la plataforma o el subdominio exclusivo de la empresa) e introduce tu **nombre de usuario** y tu **contraseña** en la página de inicio de sesión.

- ¿Olvidaste tu contraseña?: haz clic en «¿Olvidaste tu contraseña?» y restablécela con el código de verificación recibido en tu correo electrónico vinculado.
- Si tu empresa ya habilitó la identidad unificada (SSO), en la página de inicio de sesión aparecerá el botón «O inicia sesión con tu identidad corporativa»; haz clic para entrar directamente con tu cuenta corporativa.
- Primer inicio de sesión: si el administrador que creó tu cuenta no configuró un correo, el sistema mostrará una ventana obligatoria «Vincula tu correo electrónico»; compleméntala primero. Las cuentas creadas en lote deberán «Cambia tu contraseña (obligatorio)» en el primer inicio de sesión.

### 2.2 Registrarse / unirse a una empresa

Dos formas de incorporación:

- **Unirse a una empresa existente**: al registrarse, introduce el código de organización + un **código de invitación** (obligatorio; solicítalo al administrador de tu empresa) para unirte al departamento correspondiente como usuario normal. Si accedes a la página de registro mediante el subdominio exclusivo de la empresa (`código-de-empresa.dominio`), la información de la empresa se completa automáticamente; esta vía solo permite registrarse como miembro ordinario de esa empresa.
- **Crear una nueva empresa**: deja vacío el código de invitación al registrarte para crear una nueva organización; quien se registra pasa a ser su administrador de la organización y podrá invitar a compañeros (véase el capítulo 9).

El registro cuenta con la guía del asistente de IA: elige el tipo de cuenta (personal/empresa), el rol empresarial (el administrador crea la empresa / el miembro se une con un código de invitación), el sector y el rol profesional, completa los datos de la cuenta y sigue las indicaciones paso a paso; puedes volver al paso anterior. Elegir «personal» activa el plan personal; los límites de funciones se describen en [3.1 Usuarios del plan personal](#31-usuarios-del-plan-personal).

### 2.3 Conoce la interfaz

Tras iniciar sesión entras en el espacio de trabajo; las áreas principales son:

| Área | Contenido |
|------|------|
| Barra superior | Tres pestañas del espacio de trabajo: **Traducción instantánea**, **Trabajos de traducción** y **Editor comparado**; a la derecha, la insignia de saldo de créditos, la campana de notificaciones, el selector de idioma y el menú de cuenta; los administradores de departamento y superiores ven además el botón «Subir base de conocimiento» y la entrada «Consola de administración» |
| Cajón «Más» | Entradas a las páginas de autogestión, como «Mi cuenta», y el pie de página |
| Área principal | La superficie de trabajo de la pestaña activa |
| «Asistente de IA» (abajo a la derecha) | Widget de asistente inteligente disponible en todo momento (véase el capítulo 11) |

- **Cambiar el idioma de la interfaz**: desplegador de idioma en la barra superior; cada opción se muestra en su propio idioma (p. ej. «日本語», «한국어»); al elegirlo se aplica a todo el sitio de inmediato; en la primera visita se detecta automáticamente el idioma del navegador.
- **Móviles**: puedes usarlo directamente en el navegador del teléfono o la tablet; en pantallas estrechas la barra lateral de la consola se convierte automáticamente en un menú de cajón.
- **Menú de cuenta** (avatar superior derecho): entrar a la consola de administración (administrador de departamento y superior) / volver al espacio de trabajo, cambiar contraseña, cambiar correo electrónico, «Mi rol profesional» y cerrar sesión.

### 2.4 Completa tu primera traducción

1. Pega en el cuadro de entrada de «Traducción instantánea» el texto que quieras traducir;
2. Confirma el idioma de origen (de forma predeterminada, «Detección automática») y abre el selector de idiomas de destino para elegir uno o varios (las candidaturas se agrupan en «Idiomas de la base de conocimiento / Otros idiomas (IA)»);
3. Elige el modo (Rápida / Profesional) y haz clic en «Traducir»;
4. La traducción aparece en pantalla segmento a segmento en burbujas de chat: puedes copiar la traducción, ver el texto original y pedir al asistente ajustes de estilo.

Para la traducción de archivos, consulta el capítulo 5.

---

## 3. Roles y permisos

Dentro del tenant hay tres niveles de roles, con permisos crecientes:

| Rol |Quién lo ocupa | Funciones visibles |
|------|--------|----------|
| **Usuario normal** | Todos los miembros | Traducción instantánea, Trabajos de traducción (crear/ver/descargar), Editor comparado, centro de notificaciones, Asistente de IA, «Mi cuenta»; idioma de la interfaz y configuración del rol profesional |
| **Administrador de departamento** | Designado por un administrador de la organización | Todo lo del usuario normal + los menús básicos de la consola de administración: **Vista general** (con detalle de uso), **Comentarios / Aprobación** (aprobar trabajos, responder comentarios), **Base de conocimiento** (paquetes del propio departamento y paquetes interdepartamentales) y «Subir base de conocimiento» en la barra superior |
| **Administrador de la organización** | Al menos uno por empresa (creador o persona autorizada) | Todo lo del administrador de departamento + **Organización y miembros** (árbol de departamentos, miembros, códigos de invitación, presupuestos), **Llamadas externas** (claves API, webhooks, SDK), **Base de conocimiento** en todos los tipos de paquete (organización/departamento/interdepartamental), mantenimiento de nombres de marca (traducciones fijas) y configuración de la sincronización organizativa SCIM |

Notas:

- Este manual solo cubre los roles internos del tenant indicados; las funciones de operación de la plataforma quedan fuera de alcance.
- Las acciones de la pestaña «Comentarios / Aprobación» con etiquetas como «Revisión de la memoria» o archive de comentarios corresponden a procesos de la plataforma; no aparecen en la interfaz del tenant y no requieren atención.
- Para cualquier duda sobre permisos, contacta con el administrador de la organización de tu empresa.

### 3.1 Usuarios del plan personal

Si al registrarte eliges el tipo de cuenta «personal» (sin código de organización ni código de invitación), se activa una cuenta del **plan personal**: la insignia de identidad de la barra superior muestra «Plan personal», la cuenta la administras tú mismo y no existen conceptos de miembros ni departamentos empresariales.

Funciones disponibles en el plan personal:

| Función | Descripción |
|------|------|
| Los tres espacios de trabajo | Traducción instantánea, Trabajos de traducción y Editor comparado, idénticos a la versión empresarial |
| Consola de administración | Accesible desde el menú de cuenta de la barra superior: **Base de conocimiento** (sube y mantiene tu propio glosario, visible y usable solo por ti), **Llamadas externas** (emisión autónoma de claves API, para usar con la extensión de navegador, el complemento de Word y los SDK) y **Vista general** (tu propio detalle de uso) |
| Mi rol profesional | Igual que en la versión empresarial: según el puesto se ensambla automáticamente el paquete de rol (véase 7.5) |
| Páginas de autogestión | Más → «Mi cuenta»; cambiar contraseña, cambiar correo electrónico, centro de notificaciones, desactivar cuenta |

Diferencias frente a la versión empresarial:

- **No existe «Organización y miembros»**: no hay departamentos, apertura de cuentas de miembros ni presupuestos departamentales que gestionar;
- **Los trabajos no tienen fase de aprobación**: los trabajos de traducción que crees pasan directamente a la cola de ejecución, sin quedarse en «Pendiente de aprobación»;
- No hay subdominio empresarial exclusivo ni base de conocimiento compartida de la organización; las bases de conocimiento generales de la plataforma (paquetes sectoriales y de idioma y cultura) siguen aplicándose automáticamente.

Si el saldo o la cuota del plan personal no son suficientes, sigue las indicaciones que aparezcan en la interfaz.

---

## 4. Traducción instantánea (espacio de trabajo)

«Traducción instantánea» es la página predeterminada tras iniciar sesión: un cuadro de diálogo a pantalla completa, con el historial arriba y la entrada fijada abajo.

### 4.1 Área de entrada (barra de herramientas al pie del cuadro)

Una tarjeta de entrada que contiene un cuadro de texto autoadaptable de una línea y una fila de herramientas:

| Control | Descripción |
|------|------|
| Origen · Detección automática | Selecciona el idioma de origen; de forma predeterminada se detecta automáticamente |
| Idioma de destino | Abre el selector; los idiomas se agrupan en «Idiomas de la base de conocimiento / Otros idiomas (IA)», y «Más idiomas…» permite buscar en todos los idiomas; los idiomas elegidos se muestran como etiquetas y, si superan 3, se pliegan en «+n»; despliega para eliminarlos uno a uno |
| Profesional / Rápida | Modo de traducción. Rápida = borrador de IA + corrección, ligera y de alto rendimiento; Profesional = flujo completo con calibración terminológica de la base de conocimiento + evaluación de calidad + controles de barrera estrictos, adecuada para contenido de publicación formal |
| Condensar | Al marcarlo, la traducción se ajusta a la restricción de longitud; útil para títulos, textos de botones y otros escenarios con límite de caracteres |
| Traducir (botón principal) | Inicia la traducción; durante la generación puedes pulsar «Detener» |

El consumo estimado se muestra antes de enviar. Si el saldo es insuficiente o la cuota se agotó, la interfaz lo indica y señala que contactes con el administrador.

### 4.2 Conversación y resultados

- El original y la traducción se muestran uno sobre otro en la misma burbuja; la traducción aparece en pantalla segmento a segmento de forma continua.
- Cada traducción se etiqueta con su forma de obtención: **Coincidencia exacta / Coincidencia aproximada / Coincidencia semántica** (de la base de conocimiento) o **Traducción en línea** (generada por IA); los términos de la base de conocimiento que coinciden se resaltan.
- Acciones de la burbuja: «Copiar traducción», «Ver texto original»; puedes seguir enviando instrucciones para que la IA ajuste el tono o el estilo, con continuidad del contexto.
- Las fases del proceso de traducción (Búsqueda de términos → Traducción automática → Calibración de términos → Traducción completada) se muestran en la interfaz como indicaciones de etapa.

### 4.3 Gestión de la conversación

En la cabecera del cuadro de diálogo encontrarás:

- **Buscar en esta conversación (Ctrl/⌘+K)**;
- **Exportar conversación** (archivo Markdown);
- **Vaciar conversación**.

### 4.4 Visualización del uso

El consumo en la interfaz se muestra de forma unificada en «créditos», con su equivalencia aproximada en número de oraciones. Cerca del área de entrada se ven de forma permanente: el saldo (créditos ≈ oraciones), el consumo de hoy («Hoy (créditos)») y el progreso del presupuesto del departamento (si perteneces a un departamento con presupuesto asignado).

---

## 5. Trabajos de traducción (traducción de documentos)

Toda la **traducción de archivos** se inicia en la página «Trabajos de traducción» y recorre el flujo completo: crear → (según la configuración de la empresa) aprobación → traducción → entrega.

### 5.1 Crear un trabajo

Haz clic en «Crear trabajo de traducción» y elige la pestaña «Texto» o «Archivo»:

- **Texto**: pega texto largo, selecciona los idiomas de destino y el modo, y crea el trabajo directamente.
- **Archivo**: sube uno o varios archivos; puedes marcar de una vez varios idiomas de destino (los productos se empaquetan en un zip para su descarga).

Campos clave:

| Campo | Descripción |
|------|------|
| Entrega | **Conservar el formato** (predeterminado): entrega el archivo traducido con el formato original (restauración de maquetación) y, además, un archivo .md de texto plano como respaldo; **Solo texto**: extrae el cuerpo del texto localmente para traducirlo y entrega un .md con la traducción, sin garantía de maquetación (adecuado para formatos heredados doc/ppt/xls, odt/epub/rtf, etc.) |
| Modo | Rápido (sin base de conocimiento) / Revisión profesional |
| Idiomas de destino | Selección múltiple |
| Estimación | Antes de crear el trabajo se muestra «Estimado {n} oraciones · saldo {n} oraciones» |

Formatos admitidos:

| Categoría | Formatos | Entrega |
|------|------|------|
| Con restauración de maquetación | docx, pptx, xlsx, pdf, txt, csv, md | Archivo traducido en el mismo formato |
| Entrega en tabla comparativa | srt, vtt, json, yaml, etc. | xlsx original/traducción |
| Solo texto | familia heredada doc, ppt, xls; odt/ods/odp, rtf, epub | .md con la traducción |

Límites y notas:

- Cada trabajo tiene un máximo de cantidad y tamaño de archivos; los PDF de más de 40 MB o 120 páginas se rechazan con un mensaje amable que sugiere convertirlos primero a docx.
- Los PDF escaneados (solo imágenes) no son compatibles; el texto dentro de imágenes no se traduce, según la política del producto.
- Si la escritura del formato llegara a fallar, ese archivo se degrada automáticamente a entrega en .md y se notifica por mensaje interno; el trabajo completo nunca falla por este motivo.

### 5.2 Estados del trabajo y flujo

La lista «Mis trabajos» permite filtrar por estado; el significado de cada estado:

```
En cola → Traduciendo → Completado
Pendiente de aprobación → Aprobado → Traduciendo …   (cuando la empresa activa la aprobación)
        └→ Rechazado
Cualquier estado en curso → Cancelado
```

- Los trabajos en curso pueden «Cancelar»se; los finalizados pueden «Eliminar»se.
- Si tu empresa activó la aprobación, los trabajos nuevos quedan primero en «Pendiente de aprobación» y el administrador de departamento o de la organización los aprueba (Aprobado) o rechaza (Rechazado, con motivo) en la mesa de aprobación de «Comentarios / Aprobación» de la consola.

### 5.3 Ver el progreso y descargar

Abre el trabajo para ver el indicador de pasos de «Progreso de ejecución»; las fases incluyen: Extracción → Coincidencia en la base de conocimiento → Borrador de IA → Revisión profesional → Barrera estricta → Comprobación cultural → Escritura en el archivo, etc. (según el modo y el tipo de archivo).

- Al terminar, haz clic en «Descargar» para obtener la traducción; como respaldo de solo texto puedes usar «Descargar solo el texto de la traducción (.md)».
- Las tareas multi-idioma o multi-archivo se descargan como paquete zip.

### 5.4 Informe de calidad

Los trabajos finalizados en modo Revisión profesional incluyen el bloque «Informe de calidad»: conclusión general («Control de calidad superado» / «Control de calidad no superado» / «Marcado por el control de calidad»), puntuación de evaluación automática (sobre 100) y comentarios por tramos. Si no estás de acuerdo con el resultado, usa «Enviar comentarios» en el detalle del trabajo; el administrador lo verá y responderá en la consola.

### 5.5 Enlace con el Editor comparado

Desde un trabajo finalizado puedes entrar con un clic en el «Editor comparado» para revisar tramo a tramo; véase el capítulo siguiente.

---

## 6. Editor comparado

El «Editor comparado» sirve para la revisión final humana de las traducciones de los trabajos:

1. Accede desde el detalle del trabajo, o usa «Cargar» en la página del «Editor comparado» por ID/número de trabajo;
2. Original en la columna izquierda y traducción en la derecha, párrafo a párrafo, con los términos de la base de conocimiento resaltados en el lado del original;
3. Por cada tramo puedes: **aprobar** / **rechazar** (con anotación/motivo de rechazo) / modificar directamente la traducción;
4. Haz clic en «Guardar» para conservar las correcciones humanas.

Los pares de oraciones de alta calidad confirmados por personas son una fuente excelente para la memoria de traducción: los administradores de la base de conocimiento de la empresa pueden organizarlos e importarlos (véase el capítulo 7).

---

## 7. Base de conocimiento y memoria de traducción

La base de conocimiento (KB) hace que la traducción «conozca tu empresa»: unifica la terminología, reutiliza traducciones ya confirmadas y fija las formulaciones de cumplimiento normativo.

### 7.1 Cuatro niveles de contenido

| Nivel | Contenido | Uso típico |
|----|------|----------|
| L1 Términos | Pares terminológicos (incluidas las traducciones fijas de nombres de marca y de producto) | Unificación obligatoria de la terminología |
| L2 Memoria de traducción (TM) | Pares de oraciones original/traducción ya confirmados | Reutilización directa de oraciones completas o fragmentos |
| L3 Frases de seguridad | Términos prohibidos / pares de sustitución / normas de estilo | Controles de idioma y cultura: bloqueo o sustitución automática en la traducción |
| L4 Fragmentos | Expresiones sueltas y referencias de construcciones | Ejemplos de referencia para la coincidencia aproximada |

Prioridad de coincidencia al traducir: los paquetes de la cadena organizativa propia (departamento → superiores → raíz, prevalece el más cercano) > paquete de la organización > paquetes sectoriales/de idioma y cultura; los paquetes compartidos entre departamentos solo participan como nivel de respaldo si un administrador los activa.

### 7.2 Quién puede mantener qué

| Operación | Rol mínimo requerido |
|------|----------|
| «Subir base de conocimiento» en la barra superior (reconocer archivo → elegir paquete → importar) | Administrador de departamento y superior |
| Crear/mantener **paquetes de departamento** y **paquetes interdepartamentales** | Administrador de departamento y superior |
| Crear/mantener el **paquete de organización (esta empresa)**, el interruptor general de compartición entre departamentos y las autorizaciones por paquete | Administrador de la organización |
| Mantener los nombres de marca (traducciones fijas de marca/producto) | Cualquier rol con acceso al panel de base de conocimiento |
| Importar corpus bilingüe / TMX, exportar TMX | Administrador de departamento y superior |
| Mantener las «Normas de idioma y cultura (frases de seguridad)» | Administrador de departamento y superior |

### 7.3 Tipos de paquete

- **Paquete de organización (esta empresa)**: rige en toda la empresa;
- **Paquete de departamento (propio departamento)**: visible solo dentro del departamento y de la cadena organizativa, lo que logra aislamiento terminológico a nivel de departamento;
- **Paquete interdepartamental (compartido)**: por defecto no se presta; el interruptor por paquete «Compartir entre departamentos: activado/desactivado», junto con el interruptor de la política de la empresa, determina si rige para otros departamentos.

### 7.4 Formas de importación

Entra en el panel «Base de conocimiento» de la consola de administración (administrador de departamento y superior):

- **Importar archivo**: sube un glosario/documento; el sistema lo reconoce y lo escribe en el paquete elegido;
- **Importar corpus bilingüe / TMX**: escritura masiva en la memoria de traducción, compatible con el TMX exportado por las herramientas CAT dominantes (Trados/memoQ, etc.);
- **Exportar TMX**: devuelve la memoria de la empresa a tu cadena de herramientas habitual.

La plataforma además ensambla automáticamente, según el sector, los paquetes generales «paquete sectorial / paquete de idioma y cultura», sin mantenimiento por parte de la empresa; se aplican solos al traducir.

### 7.5 Mi rol profesional (todos)

Menú de cuenta → «Mi rol profesional»: elige tu puesto en el diccionario de roles predefinidos (más de 15 profesiones). Al traducir, el sistema ensambla automáticamente el paquete de rol correspondiente, adaptando la terminología y el tono a tu contexto de trabajo.

---

## 8. Cuenta y configuración personal

Accesos: el menú de cuenta de la esquina superior derecha de la barra superior y las páginas de autogestión del cajón «Más».

| Elemento | Ruta | Descripción |
|------|------|------|
| Mi cuenta | Más → «Mi cuenta» | Ver nombre de usuario/correo/rol/tenant al que perteneces; los administradores de la organización y superiores ven aquí la tarjeta de configuración de **aprovisionamiento SCIM** |
| Cambiar contraseña | Menú de cuenta → «Cambiar contraseña» | Puede exigirse obligatoriamente en el primer inicio de sesión |
| Cambiar correo electrónico | Menú de cuenta → «Cambiar correo electrónico» | Se usa para recuperar la contraseña y recibir notificaciones |
| Mi rol profesional | Menú de cuenta | Véase 7.5 |
| Notificaciones | Campana de la barra superior | Mensajes internos: estado de trabajos, resultados de aprobación, avisos de entrega degradada, etc.; admite marcar como leído uno o todos («Marcar todo como leído») |
| Desactivar cuenta | Menú de cuenta (usuario normal) | Cierre autónomo, efectivo tras confirmar según las indicaciones |
| Idioma de la interfaz | Desplegador de idioma de la barra superior | 12 idiomas, con efecto inmediato |

---

## 9. Gestión de organización y miembros (administrador de la organización)

Acceso: menú de cuenta → «Consola de administración» → «Organización y miembros» en la barra lateral.

### 9.1 Estructura organizativa

- **Nueva organización / Nuevo departamento**: permite mantener el árbol de organización multinivel de la empresa; admite renombrar.
- **Presupuesto del departamento**: usa «Asignar presupuesto mensual» para controlar el uso de traducción de los miembros de ese departamento; los miembros verán el progreso del presupuesto departamental en su interfaz.

### 9.2 Cuentas de miembros

La página «Cuentas de miembros» gestiona todas las cuentas de la empresa:

- **Añadir usuario**: crea un miembro uno a uno, indicando el rol (usuario normal / administrador de departamento / administrador de la organización) y su ubicación organizativa;
- **Importar usuarios en lote (Excel)**: abre varias cuentas a la vez; la contraseña inicial se envía por correo y el miembro deberá cambiarla en su primer inicio de sesión;
- Sobre miembros existentes: **Restablecer contraseña**, **Activar/Desactivar** la cuenta y ajustar rol y pertenencia organizativa.

### 9.3 Invitar personal

La página «Invitar personal» genera **códigos de invitación** (se admiten varios y puedes indicar el nivel organizativo al que se une cada uno). Comunica a tus compañeros el código de organización + el código de invitación; al escribirlos en la página de registro se unirán al departamento correspondiente.

### 9.4 Sincronización SCIM (opcional)

La tarjeta SCIM de «Más → «Mi cuenta»»: cuando la empresa usa proveedores de identidad como Okta o Entra ID, puede obtener el endpoint y el token de SCIM para completar el aprovisionamiento/sincronización automática de miembros; una vez activado, el personal podrá iniciar sesión sin contraseña con el botón de SSO de la página de inicio de sesión.

---

## 10. Llamadas externas e integraciones (administrador de la organización)

Acceso: consola de administración → «Llamadas externas». Tres subpestañas: **API abierta**, **Webhooks** y **SDK oficiales**.

### 10.1 Claves de la API abierta

- **Emitir clave API**: al crearla se muestra el texto completo en claro **una sola vez**; guárdala de inmediato;
- Las claves admiten **activar/desactivar, rotar, establecer un límite diario y eliminar**;
- La clave queda vinculada al tenant de tu empresa y a sus créditos: el consumo generado vía API se imputa a la misma cuenta que la interfaz web.

La API abierta ofrece: tareas de traducción asincrónicas (crear → consultar → descargar, con soporte para múltiples archivos e idiomas y productos empaquetables en zip), traducción sincrónica de textos cortos (≤5000 caracteres, ≤5 idiomas), consulta de saldo y uso, y estadísticas de la base de conocimiento. La documentación de interfaces puede consultarse en `/openapi/docs` del sitio.

### 10.2 Webhooks

Registra la URL de callback y suscríbete a eventos (p. ej. `translation.completed`) para que la plataforma notifique activamente a tus sistemas cuando termine una traducción; admite verificación de firma (X-Signature), envío de pruebas, consulta del historial de entregas y reintentos.

### 10.3 SDK oficiales y complementos

La página «SDK oficiales» ofrece guías de instalación y enlaces de descarga para todos los entornos:

| Entorno | Cómo obtenerlo | Para qué sirve |
|----|----------|------|
| Python | `pip install langcross-translator` | Traducción programática, tareas por lotes |
| TypeScript | `npm i @langcross/translator-sdk` | Integración en Node/frontend |
| Java | Distribución de código fuente Maven | Integración con sistemas internos de la empresa |
| Extensión de selección para navegador | Descarga del zip en esa misma página (sin canal de tienda de aplicaciones) | Selecciona texto en cualquier página web → pulsa el botón flotante «译» → ve la traducción en una burbuja y cópiala; atención Alt+Shift+T (Alt+T en macOS) |
| Complemento de Word | El sitio publica el manifiesto del complemento; en Word usa «Insertar → Obtener complementos → Cargar mi complemento» para instalarlo lateralmente | Traducir el texto seleccionado dentro de Word e insertar el resultado de vuelta en el documento |
| Plugin de VS Code | vscode-extension en el repositorio | Traducir in situ el texto seleccionado en el editor y consultar términos |

**Configuración de la extensión/complementos** (la lógica del popup de la extensión de selección y del panel de Word es la misma): indica la **URL del servicio** (raíz del sitio), la **clave API** (emitida en 10.1), los **idiomas de destino** y el **modo de traducción** (Rápida/Profesional), y estará lista; los términos y la memoria de traducción coinciden con los del sitio.

⚠ Si tras actualizar la extensión el estilo no cambia: sobrescribe el **mismo directorio** con el zip nuevo y pulsa el botón **Recargar** de la tarjeta de la extensión en `chrome://extensions`.

### 10.4 Límites del administrador de departamento

Si como administrador de departamento necesitas integrarte vía API, solicita al administrador de la organización que emita una clave; el menú «Llamadas externas» no se muestra a los administradores de departamento.

---

## 11. Notificaciones, comentarios y Asistente de IA

### 11.1 Centro de notificaciones (todos)

Campana de la barra superior: consulta de no leídos cada 30 segundos aprox., desplegador de mensajes internos (trabajos completados, resultados de aprobación, avisos de degradación del método de entrega, etc.), con marcado como leído individual o total.

### 11.2 Enviar y gestionar comentarios

- **Todos**: en el detalle de un trabajo finalizado, usa «Enviar comentarios» para explicar los tramos problemáticos y lo esperado.
- **Administradores de departamento/de la organización**: en «Comentarios / Aprobación» de la consola de administración ven los comentarios del tenant y pueden **responder**; en la misma página, la «Mesa de aprobación» gestiona los trabajos pendientes (Aprobar/Rechazar).
- Panel «Vista general» (administrador de departamento y superior): consulta del tablero operativo de la empresa («Vista general de la empresa» / «Vista general del departamento») y del detalle de uso (exportable a CSV).

### 11.3 Widget Asistente de IA (todos, incluidos visitantes)

El botón «Asistente de IA» de la esquina inferior derecha abre una conversación en cualquier momento: consultas sobre el producto, orientación sobre funciones y accesos directos. Al recargar la página la conversación no se pierde (caché local → recuperación automática del historial en la nube).

---

## 12. Preguntas frecuentes

**P1: El botón «Traducir» no responde / aparece un aviso de cuota insuficiente.**
El saldo actual o el presupuesto del departamento se agotó. Los usuarios normales deben contactar con el administrador de la empresa; los administradores pueden ver el detalle de uso en la consola de administración.

**P2: ¿Dónde se traduce un archivo? ¿Por qué el cuadro de chat ya no acepta adjuntos?**
La traducción de archivos se realiza exclusivamente en «Trabajos de traducción → Archivo»; el cuadro de conversación solo traduce texto.

**P3: ¿Rechazaron mi trabajo de PDF?**
Los PDF de más de 40 MB o 120 páginas deben convertirse primero a docx antes de crear el trabajo; los PDF escaneados (páginas completas de imagen) aún no son compatibles.

**P4: ¿Por qué la traducción no coincide con mi glosario?**
Comprueba que los términos se hayan importado a un paquete de la base de conocimiento visible para tu departamento (capítulo 7) y, para contenido de publicación formal, elige el modo «Profesional».

**P5: El docx entregado difiere en maquetación del original.**
El modo «Conservar el formato» preserva la maquetación en la gran mayoría de los casos; algunos diseños complejos se degradan a entrega en .md de texto plano con aviso por mensaje interno. Puedes descargar el .md y revisarlo en el Editor comparado, o enviar comentarios.

**P6: Acabo de traducir y la cifra del saldo no cambió.**
La medición se registra de forma síncrona en el momento de completar la traducción; si la página no se actualizó, refréscala una vez; si la anomalía persiste, envía un comentario.

**P7: ¿Funciona en un teléfono o tablet nuevo?**
Basta con acceder desde el navegador; la interfaz se adapta a pantallas estrechas; las extensiones y complementos están pensados sobre todo para escritorio.

**P8: El texto de la interfaz se corta o la maquetación se ve rara (p. ej. en árabe).**
Cambia primero a otro idioma para confirmar si se trata de una sola cadena y envía después un comentario por el canal de feedback con una captura.

---

## 13. Anexo

### A. Referencia rápida de nombres de la interfaz (简体中文 / Español)

| 简体中文 | Español |
|----------|---------|
| 即时翻译 | Traducción instantánea |
| 翻译工单 | Trabajos de traducción |
| 对照编辑 | Editor comparado |
| 更多 | Más |
| 我的账号 | Mi cuenta |
| 上传知识库 | Subir base de conocimiento |
| 进入管理后台 | Consola de administración |
| 通知中心 | Notificaciones |
| 专业校对 / 快速 | Profesional / Rápida |
| 缩翻 | Condensar |
| 自动检测 | Detección automática |
| 知识库语言 / 其他语言（AI翻译） | Idiomas de la base de conocimiento / Otros idiomas (IA) |
| 创建翻译工单 | Crear trabajo de traducción |
| 交付方式：还原文件模式 / 纯文案模式 | Entrega: Conservar el formato / Solo texto |
| 我的工单 | Mis trabajos |
| 排队中 / 翻译中 / 待审批 / 已批准 / 已驳回 / 已完成 / 已取消 | En cola / Traduciendo / Pendiente de aprobación / Aprobado / Rechazado / Completado / Cancelado |
| 执行进度 | Progreso de ejecución |
| 质检报告 | Informe de calidad |
| 审批台 / 批准 / 驳回 | Mesa de aprobación / Aprobar / Rechazar |
| 知识库 | Base de conocimiento |
| 组织包（本企业）/ 部门包（本部门）/ 跨部门包（共享） | Paquete de organización (esta empresa) / Paquete de departamento / Paquete interdepartamental (compartido) |
| 文件导入 / 双语语料 · TMX 导入 | Importar archivo / Importar corpus bilíngüe · TMX |
| 语言文化规范（安全句） | Normas de idioma y cultura (frases de seguridad) |
| 组织与成员 | Organización y miembros |
| 组织结构 / 邀请员工 / 成员账户 | Estructura organizativa / Invitar personal / Cuentas de miembros |
| 部门预算 | Presupuesto del departamento |
| 开通用户 / Excel 批量导入用户 | Añadir usuario / Importar usuarios en lote (Excel) |
| 邀请码管理 / 生成邀请码 | Gestión de códigos de invitación / Generar código |
| 普通用户 / 部门管理员 / 组织管理员 | Usuario / Administrador de departamento / Administrador de la organización |
| 外部调用 / 开放 API / 回调通知 / 官方 SDK | Llamadas externas / API abierta / Webhooks / SDK oficiales |
| 我的职业角色 | Mi rol profesional |
| 修改密码 / 修改邮箱 / 注销账号 | Cambiar contraseña / Cambiar correo electrónico / Desactivar cuenta |
| 积分 | Créditos |

### B. Idiomas de interfaz disponibles (12)

简体中文 · 繁體中文 · English · 日本語 · 한국어 · Deutsch · Français · Español · Português · Русский · العربية (maquetación RTL) · ไทย

### C. Resumen de atajos de teclado

| Atajo | Función |
|--------|------|
| Ctrl/⌘ + K | Traducción instantánea: buscar en esta conversación |
| Alt+Shift+T (macOS: Alt+T) | Extensión de selección para navegador: traducir la selección |
| Cmd/Ctrl+Alt+T | Plugin de VS Code: traducir la selección in situ |

### D. Compatibilidad de formatos para traducción de archivos

Consulta la tabla de [5.1 Formatos admitidos](#51-crear-un-trabajo).

---

*Este manual describe las funciones disponibles dentro del tenant. Para asuntos de operación de la plataforma, comerciales y de facturación, contacta con el administrador de tu empresa o con el equipo de servicios de la plataforma.*
