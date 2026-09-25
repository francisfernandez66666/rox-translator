# LangCross · Guia do usuário

> Público-alvo: usuários da versão pessoal e todos os membros de um tenant empresarial/de equipe (usuário comum, administrador do departamento, administrador da organização)
> Escopo do manual: cobre somente os recursos visíveis e utilizáveis dentro do tenant; não inclui funcionalidades da operação da plataforma, nem planos, recargas, faturas ou atividades promocionais.
> Os nomes da interface seguem os textos realmente exibidos pelo produto; em caso de divergência com este texto, prevalece a interface.

---

## Sumário

1. [Visão geral do produto](#1-visão-geral-do-produto)
2. [Primeiros passos](#2-primeiros-passos)
3. [Funções e permissões](#3-funções-e-permissões)
4. [Tradução imediata (espaço de trabalho)](#4-tradução-imediata-espaço-de-trabalho)
5. [Solicitações de tradução (tradução de documentos)](#5-solicitações-de-tradução-tradução-de-documentos)
6. [Edição lado a lado](#6-edição-lado-a-lado)
7. [Base de conhecimento e memória de tradução](#7-base-de-conhecimento-e-memória-de-tradução)
8. [Conta e configurações pessoais](#8-conta-e-configurações-pessoais)
9. [Gestão da organização e de membros (administrador da organização)](#9-gestão-da-organização-e-de-membros-administrador-da-organização)
10. [Chamadas externas e integrações (administrador da organização)](#10-chamadas-externas-e-integrações-administrador-da-organização)
11. [Notificações, feedback e assistente de IA](#11-notificações-feedback-e-assistente-de-ia)
12. [Perguntas frequentes](#12-perguntas-frequentes)
13. [Apêndice](#13-apêndice)

---

## 1. Visão geral do produto

**LangCross (能言)** é uma plataforma de colaboração em traduções com IA para empresas e equipes, que integra três formas de tradução e um sistema corporativo de ativos de linguagem:

| Recurso | Descrição |
|------|------|
| Tradução imediata | Tradução de texto em formato de conversa: digite e traduza, com envio para vários idiomas de destino de uma só vez |
| Solicitações de tradução | Envie arquivos docx / pptx / xlsx / pdf e execute o fluxo completo «criar → aprovar → traduzir → entregar», com o arquivo traduzido entregue no formato original |
| Edição lado a lado | Revisão do original/tradução em duas colunas, trecho a trecho: é possível editar, aprovar ou reprovar com anotações |
| Base de conhecimento / Memória de tradução (KB/TM) | Termos da empresa, pares bilíngues de frases e nomes de marca com tradução fixa entram na base de conhecimento e são aplicados automaticamente durante a tradução |

Principais características:

- **Mais de 40 idiomas em tradução mútua**; a própria interface está disponível em 12 idiomas (简体中文、繁體中文、English、日本語、한국어、Deutsch、Français、Español、Português、Русский、العربية、ไทย); a interface em árabe usa layout da direita para a esquerda.
- **Dois modos de tradução**: «Rápido», leve e eficiente; «Revisão profissional», com calibragem de termos da base de conhecimento, avaliação de qualidade e portões de verificação obrigatória, indicada para conteúdo a ser publicado.
- **Isolamento empresarial**: os dados, as bases de conhecimento e os membros de cada tenant são isolados entre si; dentro da organização, bases de conhecimento e orçamentos são ainda isolados por departamento.
- **Consistência entre superfícies**: além da versão web, há extensão de navegador para tradução por seleção, suplemento do Word, plugin do VS Code e API/SDK abertos — a terminologia e as traduções seguem a mesma fonte da versão web.

---

## 2. Primeiros passos

### 2.1 Entrar

Abra o endereço de acesso fornecido pela sua empresa (o endereço da plataforma ou o subdomínio dedicado da empresa) e informe **nome de usuário** e **senha** na página de login.

- Esqueceu a senha: clique em «Recuperar senha» e redefina por meio de um código de verificação enviado ao e-mail vinculado.
- Quando a empresa usa identidade unificada (SSO), a página de login exibe o botão «Ou entre com a identidade da empresa» — clique para entrar diretamente com a conta corporativa.
- Primeiro login: se o administrador não definiu um e-mail ao criar sua conta, o sistema exibe uma janela não dispensável «Vincule seu e-mail»; conclua o vínculo primeiro. Contas abertas em lote devem «alterar a senha no primeiro login».

### 2.2 Criar conta / ingressar em uma empresa

Duas formas de acesso:

- **Ingressar em uma empresa existente**: no cadastro, informe o código da organização + um **código de convite** (obrigatório; solicite ao administrador da sua empresa) para ingressar no departamento correspondente como usuário comum. Ao acessar a página de cadastro pelo subdomínio dedicado da empresa (`código-da-empresa.dominio`), as informações da empresa são preenchidas automaticamente; essa entrada permite cadastrar somente membros comuns dessa empresa.
- **Criar uma nova empresa**: deixe o código de convite vazio para criar uma nova organização — quem se cadastra se torna o administrador da organização e pode continuar convidando colegas (ver capítulo 9).

O fluxo de cadastro conta com a orientação do assistente de IA: escolha o tipo de conta (pessoal/empresa) → a identidade na empresa (administrador cria uma empresa / membro ingressa com código de convite) → setor e função profissional → dados da conta; siga as instruções passo a passo, com opção de voltar à etapa anterior. Escolher «Pessoal» abre a versão pessoal — os limites de recursos estão em [3.1 Usuários da versão pessoal](#31-usuários-da-versão-pessoal).

### 2.3 Conheça a interface

Após entrar, você chega ao espaço de trabalho. As principais áreas:

| Área | Conteúdo |
|------|------|
| Barra superior | Três abas do espaço de trabalho: **Tradução imediata**, **Solicitações de tradução**, **Edição lado a lado**; à direita: selo de saldo de pontos, sino de notificações, seletor de idioma e menu da conta; administradores de departamento e acima também veem o botão «Enviar base de conhecimento» e a entrada «Console de administração» |
| Gaveta «Mais» | Entradas de páginas de autoatendimento, como Minha conta, e o rodapé |
| Área principal | A superfície de trabalho da aba ativa |
| «Assistente de IA» (canto inferior direito) | Widget de assistente inteligente sempre disponível (ver capítulo 11) |

- **Trocar o idioma da interface**: o menu suspenso de idiomas na barra superior lista cada idioma em sua própria grafia (por exemplo, 「日本語」「한국어」); a escolha vale imediatamente para todo o site; na primeira visita, o idioma é detectado automaticamente pelo navegador.
- **Dispositivos móveis**: funciona diretamente no navegador de celulares/tablets; em telas estreitas, a barra lateral da administração vira um menu de gaveta.
- **Menu da conta** (avatar no canto superior direito): entrar no console de administração (administradores de departamento e acima) / voltar ao espaço de trabalho, alterar senha, alterar e-mail, minha função, sair.

### 2.4 Conclua a primeira tradução

1. Cole na caixa de entrada da «Tradução imediata» o texto a traduzir;
2. Confirme o idioma de origem (padrão: «Detecção automática») e escolha um ou mais idiomas no seletor de idioma de destino (as opções são agrupadas em «Idiomas da base / Outros idiomas»);
3. Selecione o modo (Rápido / Revisão profissional) e clique em «Traduzir»;
4. A tradução aparece na tela em bolhas de conversa, trecho a trecho — copie a tradução, veja o original ou continue perguntando para ajustar o estilo.

Para tradução de arquivos, veja o capítulo 5.

---

## 3. Funções e permissões

Há três níveis de função dentro do tenant, com permissões crescentes:

| Função | Quem | Recursos visíveis |
|------|--------|----------|
| **Usuário comum** | Todos os membros | Tradução imediata, Solicitações de tradução (criar/ver/baixar), Edição lado a lado, central de notificações, assistente de IA, Minha conta; idioma da interface e configurações de função profissional |
| **Administrador do departamento** | Designado pelo administrador da organização | Todos os recursos do usuário comum + os menus básicos do console de administração: **Visão geral** (com detalhes de uso), **Feedback/Aprovação** (aprovar solicitações, responder feedbacks), **Base de conhecimento** (pacotes do próprio departamento e interdepartamentais), botão «Enviar base de conhecimento» na barra superior |
| **Administrador da organização** | Pelo menos um por empresa (criador ou pessoa designada) | Todos os recursos do administrador do departamento + **Organização e membros** (árvore de departamentos, membros, códigos de convite, orçamentos), **Chamadas externas** (chaves de API, webhooks, SDKs), **Base de conhecimento** em todos os tipos de pacote (organização/departamento/interdepartamental), manutenção de nomes de marca (traduções fixas) e configuração da sincronização organizacional via SCIM |

Observações:

- Este manual cobre apenas as funções de tenant acima; as funcionalidades da operação da plataforma estão fora do escopo.
- Operações com os rótulos «revisão de memória» e «concluir arquivamento de feedback» na aba «Feedback/Aprovação» pertencem a processos da plataforma, não aparecem na interface do tenant e dispensam atenção.
- Em caso de dúvida sobre permissões, contate o administrador da organização da sua empresa.

### 3.1 Usuários da versão pessoal

Ao escolher o tipo de conta «Pessoal» no cadastro (sem código de organização nem código de convite), você abre a **versão pessoal** — o selo de identidade na barra superior mostra «Versão pessoal»; a conta é gerenciada apenas por você, sem os conceitos de membros e departamentos da empresa.

Recursos disponíveis na versão pessoal:

| Recurso | Descrição |
|------|------|
| Os três espaços de trabalho | Tradução imediata, Solicitações de tradução e Edição lado a lado, exatamente iguais à versão empresarial |
| Console de administração | Acessável pelo menu da conta: **Base de conhecimento** (envie e mantenha seu próprio glossário, visível e utilizável somente por você), **Chamadas externas** (emissão autônoma de chaves de API, para uso com a extensão de navegador, o suplemento do Word e os SDKs), **Visão geral** (seus detalhes de uso) |
| Minha função | Igual à versão empresarial — os pacotes de função são montados automaticamente conforme o cargo (ver 7.5) |
| Páginas de autoatendimento | Mais → Minha conta; alterar senha, alterar e-mail, central de notificações, encerrar a conta |

Diferenças em relação à versão empresarial:

- **Sem «Organização e membros»**: não existem departamentos, abertura de membros nem orçamentos por departamento;
- **Solicitações sem etapa de aprovação**: uma solicitação de tradução criada entra direto na fila de execução, sem ficar em «Aguardando aprovação»;
- Sem subdomínio empresarial dedicado nem base de conhecimento compartilhada da organização; os pacotes de indústria e de idioma/cultura da plataforma continuam valendo automaticamente.

Quando a cota ou o saldo da versão pessoal estiver insuficiente, basta seguir a instrução exibida na tela.

---

## 4. Tradução imediata (espaço de trabalho)

«Tradução imediata» é a página padrão após o login: um diálogo de tela inteira, com o histórico em cima e a entrada fixa embaixo.

### 4.1 Área de entrada (barra de ferramentas ao pé da caixa)

Um cartão de entrada contendo uma caixa de texto monolinear com expansão automática e uma barra de ferramentas em linha única:

| Controle | Descrição |
|------|------|
| Origem · Detecção automática | Escolhe o idioma de origem; por padrão, a detecção é automática |
| Idioma de destino | Abre o seletor; os idiomas são agrupados em «Idiomas da base / Outros idiomas (tradução IA)», e «Mais idiomas…» permite buscar todos os idiomas; os idiomas escolhidos aparecem como etiquetas — além de 3, são recolhidos em «+n»; expanda para removê-los um a um |
| Revisão profissional / Rápido | Modo de tradução. Rápido = primeira tradução por IA + revisão, leve e com alto rendimento; Revisão profissional = fluxo completo com calibragem de termos da base de conhecimento, avaliação de qualidade e portão de verificação obrigatória, indicado para conteúdo de publicação oficial |
| Tradução condensada | Quando marcada, a tradução é contida por limites de comprimento, indicada para títulos, textos de botões e outros cenários com limite de caracteres |
| Traduzir (botão principal) | Inicia a tradução; durante a geração é possível clicar em «Parar geração» |

A estimativa de consumo é exibida antes do envio. Quando o saldo ou a cota se esgotam, a interface exibe um aviso e orienta procurar o administrador.

### 4.2 Conversa e resultados

- O original e a tradução aparecem empilhados na mesma bolha; a tradução flui na tela trecho a trecho.
- Cada tradução traz indicada a forma como foi produzida: **correspondência exata / correspondência aproximada / similaridade semântica** (da base de conhecimento) ou **tradução on-line** (gerada por IA); termos correspondentes da base de conhecimento ficam destacados.
- Ações na bolha: copiar a tradução, ver o original; continue enviando instruções para que a IA ajuste tom e estilo — o contexto é mantido.
- As etapas do processo de tradução (busca de termos → tradução automática → calibragem de termos → tradução concluída) aparecem na interface como indicações de fase.

### 4.3 Gerenciamento da conversa

No topo do diálogo há:

- **Buscar mensagens desta conversa** (atalho Ctrl/⌘+K);
- **Exportar o registro da conversa** (arquivo Markdown);
- **Limpar a conversa**.

### 4.4 Exibição de uso

O consumo na interface é exibido uniformemente em «pontos», com a conversão aproximada em «número de frases». Perto da área de entrada ficam sempre visíveis: saldo (pontos ≈ frases), consumo do dia e o progresso do orçamento do departamento (quando você está vinculado a um departamento com orçamento).

---

## 5. Solicitações de tradução (tradução de documentos)

Toda **tradução de arquivos** é iniciada na aba «Solicitações de tradução» e segue o fluxo completo: criar → (conforme a configuração da empresa) aprovar → traduzir → entregar.

### 5.1 Criar uma solicitação

Clique em «Criar ticket de tradução» e escolha a aba «Texto» ou «Arquivo»:

- **Texto**: cole um texto longo, escolha o idioma de destino e o modo, e crie a solicitação diretamente.
- **Arquivo**: envie um ou mais arquivos; é possível marcar vários idiomas de destino de uma vez (os resultados são empacotados em zip para download).

Campos principais:

| Campo | Descrição |
|------|------|
| Forma de entrega | **Restaurar formato** (padrão): entrega o arquivo traduzido no formato original (com restauração do layout), acompanhado de um arquivo .md somente-texto como fallback; **Somente texto**: extrai o corpo do texto localmente para traduzir e entrega um .md traduzido, sem garantia de layout (indicado para formatos legados doc/ppt/xls, odt/epub/rtf etc.) |
| Modo | Rápido (sem base de conhecimento) / Revisão profissional |
| Idioma de destino | Seleção múltipla permitida |
| Estimativa | Antes de criar, exibe «Estimativa: {n} frases · saldo {n} frases» |

Suporte a formatos de arquivo:

| Categoria | Formatos | Entrega |
|------|------|------|
| Com restauração de layout | docx, pptx, xlsx, pdf, txt, csv, md | Arquivo traduzido no mesmo formato |
| Entrega em tabela bilíngue | srt, vtt, json, yaml etc. | Xlsx com original/tradução lado a lado |
| Somente texto | formatos legados da família doc, ppt, xls, odt/ods/odp, rtf, epub | .md traduzido |

Limites e observações:

- Há limites de quantidade e tamanho de arquivos por solicitação; PDFs com mais de 40 MB ou 120 páginas são recusados com uma mensagem sugerindo converter para docx primeiro.
- PDFs digitalizados (páginas em imagem) não são compatíveis; texto dentro de imagens não é traduzido, conforme a estratégia do produto.
- Se a gravação do layout falhar em algum arquivo, ele é automaticamente rebaixado para entrega em .md, com aviso na central de notificações — a solicitação como um todo não falha por isso.

### 5.2 Estados e fluxo da solicitação

A lista «Meus tickets» pode ser filtrada por estado; o significado de cada estado:

```
Na fila → Traduzindo → Concluído
Aguardando aprovação → Aprovado → Traduzindo …    (quando a aprovação está ativada na empresa)
                 └→ Rejeitado
Qualquer estado em andamento → Cancelado
```

- Solicitações em andamento podem ser «Canceladas»; as já encerradas podem ser «Excluídas».
- Se a sua empresa ativou a aprovação, a nova solicitação fica em «Aguardando aprovação», e o administrador do departamento/organização a aprova (Aprovado) ou repróva com motivo (Rejeitado) na bancada de aprovação de «Feedback/Aprovação» no console de administração.

### 5.3 Ver o progresso e baixar

Abra a solicitação para ver o indicador de etapas «Progresso», com fases como: extração e análise → correspondência na base de conhecimento → primeira tradução por IA → revisão profissional → verificação obrigatória → verificação cultural → gravação no arquivo etc. (exibidas conforme o modo e o tipo de arquivo).

- Ao concluir, clique em «Baixar» para obter a tradução; o fallback somente-texto pode ser obtido em «Baixar somente o texto traduzido (.md)».
- Tarefas com vários idiomas/arquivos são baixadas como pacote zip.

### 5.4 Relatório de qualidade

As solicitações concluídas no modo Revisão profissional trazem o bloco «Relatório de qualidade»: conclusão geral (Aprovado no controle de qualidade / Reprovado no controle de qualidade / Sinalizado no controle de qualidade), pontuação da avaliação automática (nota máxima 100) e explicação por trecho. Em caso de discordância, use «Enviar feedback» no detalhe da solicitação; o administrador verá e responderá no console de administração.

### 5.5 Integração com a Edição lado a lado

Uma solicitação concluída pode ser aberta com um clique na «Edição lado a lado» para revisão trecho a trecho — veja o próximo capítulo.

---

## 6. Edição lado a lado

«Edição lado a lado» serve à revisão final humana das traduções de solicitações:

1. Acesse pela página de detalhe da solicitação ou use «Carregar» na aba «Edição lado a lado» pelo ID/número da solicitação;
2. Original na coluna esquerda, tradução na direita, trecho a trecho; os termos da base de conhecimento ficam destacados no lado do original;
3. Por trecho: **Aprovar** / **Reprovar** (preencher anotação/motivo da rejeição) / editar a tradução diretamente;
4. Clique em «Salvar» para registrar as revisões manuais.

Os pares de frases validados por humanos são uma fonte valiosa de memória de tradução — os administradores da base de conhecimento da empresa podem organizá-los e importá-los (ver capítulo 7).

---

## 7. Base de conhecimento e memória de tradução

A base de conhecimento (KB) faz a tradução «conhecer a sua empresa»: unifica a terminologia, reutiliza traduções já confirmadas e trava expressões de conformidade.

### 7.1 Quatro camadas

| Camada | Conteúdo | Uso típico |
|----|------|----------|
| L1 Termos | Pares de termos (incluindo nomes de marca e de produto com tradução fixa) | Unificação obrigatória do vocabulário |
| L2 Memória de tradução (TM) | Pares de frases original/tradução já confirmados | Reutilização de frases inteiras ou fragmentos |
| L3 Frases de segurança | Palavras proibidas / pares de substituição / normas de estilo | Verificação obrigatória de idioma e cultura: bloqueio ou substituição automática na tradução |
| L4 Fragmentos | Expressões avulsas e referências de estruturas frásicas | Exemplos para a correspondência aproximada |

Prioridade de correspondência na tradução: pacotes na cadeia da sua organização (departamento próprio → superiores → raiz; o mais próximo prevalece) > pacote da organização > pacotes de indústria/idioma-cultura; os pacotes interdepartamentais compartilhados entram como camada de fallback somente quando habilitados pelo administrador.

### 7.2 Quem pode manter o quê

| Operação | Função exigida |
|------|----------|
| «Enviar base de conhecimento» na barra superior (reconhecer arquivo → escolher pacote → importar) | Administrador do departamento ou acima |
| Criar/manter **pacotes do departamento** e **interdepartamentais** | Administrador do departamento ou acima |
| Criar/manter o **pacote da organização (esta empresa)**, o interruptor geral de compartilhamento interdepartamental e as autorizações por pacote | Administrador da organização |
| Manter «Nomes de marca» (traduções fixas de marca/produto) | Qualquer função com acesso ao painel da base de conhecimento |
| Importar corpus bilíngue / TMX e exportar TMX | Administrador do departamento ou acima |
| Manter «Regras de idioma e cultura (frases de segurança)» | Administrador do departamento ou acima |

### 7.3 Tipos de pacote

- **Pacote da organização (esta empresa)**: vale para toda a empresa;
- **Pacote do departamento (este departamento)**: visível apenas dentro do departamento e da cadeia organizacional, garantindo o isolamento de terminologia por departamento;
- **Pacote interdepartamental (compartilhado)**: por padrão não é emprestado; o interruptor «Interdepartamental: ativado/desativado» do pacote, somado à política da empresa, decide se ele vale para outros departamentos.

### 7.4 Formas de importação

No console de administração, no painel «Base de conhecimento» (administradores de departamento e acima):

- **Importação de arquivo**: envie um glossário/documento; o sistema o reconhece e grava as entradas no pacote escolhido;
- **Importação de texto bilíngue / TMX**: grava em massa na memória de tradução, compatível com TMX exportado pelas principais ferramentas CAT (Trados/memoQ etc.);
- **Exportação de TMX**: leve a memória da empresa de volta para seu fluxo de ferramentas atual.

A plataforma também monta automaticamente pacotes genéricos de indústria e de idioma-cultura conforme o setor — sem necessidade de manutenção; eles valem automaticamente na tradução.

### 7.5 Minha função (todos)

Menu da conta → «Minha função»: escolha seu cargo no dicionário de funções predefinidas (mais de 15 profissões). Na tradução, o sistema monta automaticamente o pacote de função correspondente, ajustando a terminologia e o tom ao seu contexto de trabalho.

---

## 8. Conta e configurações pessoais

Entradas: o menu da conta no canto superior direito da barra superior e as páginas de autoatendimento na gaveta «Mais».

| Item | Caminho | Descrição |
|------|------|------|
| Minha conta | Mais → Minha conta | Veja nome de usuário/e-mail/função/tenant; administradores da organização e acima também veem aqui o cartão de configuração da **sincronização organizacional SCIM** |
| Alterar senha | Menu da conta → Alterar senha | Pode ser exigida no primeiro login |
| Alterar e-mail | Menu da conta → Alterar e-mail | Usado para recuperar a senha e receber notificações |
| Minha função | Menu da conta | Ver 7.5 |
| Central de notificações | Sino na barra superior | Avisos internos: estado de solicitações, resultados de aprovação, lembretes de entrega rebaixada etc.; marque um item ou todos como lidos |
| Encerrar a conta | Menu da conta (usuário comum) | Encerramento autônomo, efetivado após confirmação conforme as instruções |
| Idioma da interface | Menu suspenso de idiomas na barra superior | 12 idiomas, vale imediatamente |

---

## 9. Gestão da organização e de membros (administrador da organização)

Entrada: menu da conta → «Console de administração» → «Organização e membros» à esquerda.

### 9.1 Estrutura organizacional

- **Nova organização / novo departamento**: mantenha a árvore organizacional multinível da empresa; a renomeação é suportada.
- **Orçamento do departamento**: «Alocar orçamento mensal» por departamento para controlar o volume de tradução dos membros daquele departamento; a interface dos membros mostra o progresso do orçamento.

### 9.2 Membros

A página «Membros» gerencia todas as contas da empresa:

- **Adicionar usuário**: crie um membro por vez, definindo a função (usuário comum / administrador do departamento / administrador da organização) e a organização de vínculo;
- **Importar usuários em lote (Excel)**: abra várias contas de uma vez — as senhas iniciais são enviadas por e-mail e devem ser alteradas no primeiro login;
- Para membros existentes: **redefinir senha**, **ativar/desativar** contas e ajustar função e vínculo organizacional.

### 9.3 Convidar equipe

Na página «Convidar equipe» gere **códigos de convite** (vários permitidos, cada um vinculado a um nível da organização). Informe aos colegas o código da organização + o código de convite; ao preenchê-los na página de cadastro, eles entram no departamento correspondente.

### 9.4 Sincronização organizacional via SCIM (opcional)

O cartão SCIM em «Mais → Minha conta»: se a empresa usa provedores de identidade como Okta ou Entra ID, obtenha o endpoint e o token SCIM para automatizar a abertura/sincronização de membros; ativado isso, os colaboradores podem entrar sem senha pelo botão de SSO da página de login.

---

## 10. Chamadas externas e integrações (administrador da organização)

Entrada: console de administração → «Chamadas externas». Três subpáginas: **API aberta**, **Webhooks** e **SDKs oficiais**.

### 10.1 Chaves de API da API aberta

- **Emitir uma chave de API**: o texto completo em claro é exibido **uma única vez** na criação — guarde-o imediatamente;
- As chaves admitem **ativar/desativar, rotação, limite diário e exclusão**;
- A chave é vinculada ao tenant da sua empresa e à contabilidade em pontos — o consumo gerado pela API é debitado da mesma conta da versão web.

A API aberta oferece: tarefas assíncronas de tradução (criar → consultar → baixar, com múltiplos arquivos e idiomas, resultados empacotáveis em zip), tradução síncrona de textos curtos (até 5.000 caracteres e até 5 idiomas), consulta de saldo e uso e estatísticas da base de conhecimento. A documentação da interface pode ser consultada em `/openapi/docs` no site.

### 10.2 Webhooks (notificações de callback)

Cadastre a URL de callback e assine eventos (por exemplo, `translation.completed`) para que a plataforma notifique ativamente seus sistemas quando a tradução terminar; há verificação de assinatura (X-Signature), envio de teste, histórico de entregas e tentativas de reenvio.

### 10.3 SDKs oficiais e plugins

A página «SDKs oficiais» traz guias de instalação e entradas de download para as três plataformas, além dos plugins:

| Plataforma | Como obter | Para que serve |
|----|----------|------|
| Python | baixe o `.whl` (ou o fonte `.tar.gz`) na página "SDK oficial" deste site e instale localmente: `pip install <arquivo baixado>` (hospedado em `/sdk/` deste site; não publicado em registros externos) | Tradução programática, tarefas em lote  |
| TypeScript | baixe o `.tgz` na página "SDK oficial" deste site e instale localmente: `npm install <arquivo baixado>` (idem, hospedado em `/sdk/` deste site) | Integração em Node/front-end  |
| Java | Distribuição de código-fonte via Maven | Integração com sistemas internos da empresa |
| Extensão de navegador (tradução por seleção) | Download do zip nessa própria página (sem canal de loja de extensões) | Selecione texto em qualquer página → clique no botão flutuante 「译」 → veja a tradução no balão e copie; atalho Alt+Shift+T (Alt+T no macOS) |
| Suplemento do Word | O site fornece o manifesto do suplemento; no Word, use «Inserir → Obter Suplementos → Carregar Meu Suplemento» para carregá-lo lateralmente | Traduza o texto selecionado dentro do Word e insira o resultado de volta no documento |
| Plugin do VS Code | vscode-extension no repositório | Tradução in loco do texto selecionado no editor e consulta de termos |

**Configuração da extensão/suplemento** (o popup da extensão de seleção e o painel do Word seguem a mesma lógica): preencha o **endereço do serviço** (URL raiz do site), a **chave de API** (emitida em 10.1 deste capítulo), os **idiomas de destino** e o **modo de tradução** (Rápido/Revisão profissional) e comece a usar — a terminologia e a memória de tradução são as mesmas do site.

⚠ Se, após atualizar a extensão, o visual não mudar: substitua o **mesmo diretório** pelo novo zip e clique no botão **Atualizar (Reload)** no cartão da extensão em `chrome://extensions`.

### 10.4 Limites do administrador do departamento

Se o administrador do departamento precisar integrar via API, solicite ao administrador da organização a emissão de uma chave; o menu «Chamadas externas» não é exibido para administradores de departamento.

---

## 11. Notificações, feedback e assistente de IA

### 11.1 Central de notificações (todos)

O sino na barra superior consulta itens não lidos com frequência de dezenas de segundos; o menu suspenso mostra os avisos internos (solicitações concluídas, resultados de aprovação, lembretes de rebaixamento da forma de entrega etc.), com opção de marcar um item ou todos como lidos.

### 11.2 Enviar e tratar feedback

- **Todos**: no detalhe de uma solicitação concluída, use «Enviar feedback» descrevendo os trechos problemáticos e o resultado esperado.
- **Administradores de departamento/organização**: no console de administração, «Feedback/Aprovação» exibe os feedbacks do tenant e permite **responder**; na mesma aba, a «Bancada de aprovação» trata as solicitações pendentes (Aprovar/Reprovar).
- Painel «Visão geral» (administradores de departamento e acima): veja o painel operacional da empresa e os detalhes de uso (os detalhes de uso podem ser exportados em CSV).

### 11.3 Assistente de IA (todos, inclusive visitantes)

O botão «Assistente de IA» no canto inferior direito abre um chat a qualquer momento: dúvidas sobre o produto, orientação de recursos e atalhos. Ao recarregar a página, a conversa não se perde (cache local → histórico na nuvem restaurado automaticamente).

---

## 12. Perguntas frequentes

**Q1: O botão «Traduzir» não responde / aparece mensagem de cota insuficiente?**
O saldo atual ou o orçamento do departamento acabou. Usuários comuns: procurem o administrador da empresa; os administradores podem consultar os detalhes de uso no console de administração.

**Q2: Onde traduzo arquivos? Por que a caixa de conversa não aceita anexos?**
A tradução de arquivos é feita exclusivamente em «Solicitações de tradução → Arquivo»; o chat faz apenas tradução de texto puro.

**Q3: Meu PDF foi recusado na tradução?**
Converta primeiro em docx os PDFs com mais de 40 MB ou 120 páginas; PDFs digitalizados (páginas inteiras em imagem) ainda não são compatíveis.

**Q4: Por que a tradução não segue meu glossário?**
Confira se os termos foram importados para um pacote da base de conhecimento visível ao seu departamento (capítulo 7) e use o modo «Revisão profissional» para conteúdo oficial.

**Q5: O layout do docx entregue difere do original?**
O modo «Restaurar formato» preserva o layout na grande maioria dos casos; layouts complexos eventuais são rebaixados para entrega em texto .md, com aviso interno. Baixe o .md e revise na Edição lado a lado, ou envie feedback.

**Q6: A tradução acabou de ser feita e o saldo não mudou?**
A contabilização é gravada no instante da conclusão da tradução; atualize a página manualmente uma vez; se a divergência persistir, envie feedback.

**Q7: Funciona em celular/tablet?**
Basta acessar pelo navegador; a interface se adapta a telas estreitas; extensões e suplementos são focados no desktop.

**Q8: O texto aparece cortado ou o layout está estranho (árabe etc.)?**
Troque para outro idioma para confirmar se é uma entrada isolada e envie um screenshot pelo canal de feedback.

---

## 13. Apêndice

### A. Consulta rápida de nomes da interface (简体中文 | Português)

| 简体中文 | Português |
|----------|---------|
| 即时翻译 | Tradução imediata |
| 翻译工单 | Solicitações de tradução |
| 对照编辑 | Edição lado a lado |
| 更多 | Mais |
| 我的账号 | Minha conta |
| 上传知识库 | Enviar base de conhecimento |
| 进入管理后台 | Console de administração |
| 通知中心 | Central de notificações |
| 专业校对 / 快速 | Revisão profissional / Rápido |
| 缩翻 | Tradução condensada |
| 自动检测 | Detecção automática |
| 知识库语言 / 其他语言（AI翻译） | Idiomas da base / Outros idiomas (tradução IA) |
| 创建翻译工单 | Criar ticket de tradução |
| 交付方式：还原文件模式 / 纯文案模式 | Forma de entrega: Restaurar formato / Somente texto |
| 我的工单 | Meus tickets |
| 排队中 / 翻译中 / 待审批 / 已批准 / 已驳回 / 已完成 / 已取消 | Na fila / Traduzindo / Aguardando aprovação / Aprovado / Rejeitado / Concluído / Cancelado |
| 执行进度 | Progresso |
| 质检报告 | Relatório de qualidade |
| 审批台 / 批准 / 驳回 | Bancada de aprovação / Aprovar / Reprovar |
| 知识库 | Base de conhecimento |
| 组织包（本企业）/ 部门包（本部门）/ 跨部门包（共享） | Pacote da organização (esta empresa) / Pacote do departamento (este departamento) / Pacote interdepartamental (compartilhado) |
| 文件导入 / 双语语料 · TMX 导入 | Importação de arquivo / Importação de texto bilíngue · TMX |
| 语言文化规范（安全句） | Regras de idioma e cultura (frases de segurança · verificação) |
| 组织与成员 | Organização e membros |
| 组织结构 / 邀请员工 / 成员账户 | Estrutura organizacional / Convidar equipe / Membros |
| 部门预算 | Orçamento do departamento |
| 开通用户 / Excel 批量导入用户 | Adicionar usuário / Importar usuários em lote (Excel) |
| 邀请码管理 / 生成邀请码 | Códigos de convite / Gerar código |
| 普通用户 / 部门管理员 / 组织管理员 | Usuário / Administrador do departamento / Administrador da organização |
| 外部调用 / 开放 API / 回调通知 / 官方 SDK | Chamadas externas / API aberta / Webhooks / SDKs oficiais |
| 我的职业角色 | Minha função |
| 修改密码 / 修改邮箱 / 注销账号 | Alterar senha / Alterar e-mail / Encerrar a conta |
| 积分 | Pontos |

### B. Idiomas da interface (12)

简体中文 · 繁體中文 · English · 日本語 · 한국어 · Deutsch · Français · Español · Português · Русский · العربية (layout RTL) · ไทย

### C. Resumo dos atalhos de teclado

| Atalho | Função |
|--------|------|
| Ctrl/⌘ + K | Tradução imediata: buscar mensagens desta conversa |
| Alt+Shift+T (macOS: Alt+T) | Extensão de navegador (tradução por seleção): traduzir o texto selecionado |
| Cmd/Ctrl+Alt+T | Plugin do VS Code: traduzir a seleção in loco |

### D. Suporte a formatos na tradução de arquivos

Veja a tabela em [5.1 Criar uma solicitação](#51-criar-uma-solicitação).

---

*Este manual descreve os recursos disponíveis dentro do tenant. Assuntos de operação da plataforma, comerciais e de cobrança devem ser tratados com o administrador da sua empresa ou com a equipe de serviços da LangCross.*
