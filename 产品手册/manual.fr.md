# LangCross (能言) · Guide de l'utilisateur

> Public : utilisateurs de la version personnelle et tous les membres d'un tenant entreprise/équipe (utilisateur ordinaire, administrateur de département, administrateur d'organisation)
> Périmètre : uniquement les fonctions visibles et utilisables à l'intérieur du tenant ; les fonctions d'exploitation de la plateforme, les forfaits, le rechargement, les factures et les activités promotionnelles ne sont pas inclus.
> Les noms d'interface suivent les libellés réellement affichés par le produit ; en cas d'écart avec ce document, l'interface fait foi.

---

## Table des matières

1. [Présentation du produit](#1-présentation-du-produit)
2. [Premiers pas](#2-premiers-pas)
3. [Rôles et permissions](#3-rôles-et-permissions)
4. [Traduction instantanée (atelier)](#4-traduction-instantanée-atelier)
5. [Tickets de traduction (traduction de documents)](#5-tickets-de-traduction-traduction-de-documents)
6. [Édition en vis-à-vis](#6-édition-en-vis-à-vis)
7. [Base de connaissances et mémoire de traduction](#7-base-de-connaissances-et-mémoire-de-traduction)
8. [Compte et paramètres personnels](#8-compte-et-paramètres-personnels)
9. [Gestion de l'organisation et des membres (administrateur d'organisation)](#9-gestion-de-lorganisation-et-des-membres-administrateur-dorganisation)
10. [Appels externes et intégrations (administrateur d'organisation)](#10-appels-externes-et-intégrations-administrateur-dorganisation)
11. [Notifications, retours et assistant IA](#11-notifications-retours-et-assistant-ia)
12. [Questions fréquentes](#12-questions-fréquentes)
13. [Annexe](#13-annexe)

---

## 1. Présentation du produit

**LangCross (能言)** est une plateforme de collaboration de traduction par IA destinée aux entreprises et aux équipes. Elle réunit trois modes de traduction et un système d'assets linguistiques d'entreprise :

| Fonction | Description |
|------|------|
| Traduction instantanée | Traduction de texte en mode conversationnel : traduction à la saisie, avec possibilité de traduire vers plusieurs langues cibles d'un coup |
| Tickets de traduction | Téléversez des fichiers docx / pptx / xlsx / pdf et suivez le flux complet « création → approbation → traduction → livraison », avec restitution du fichier traduit dans le format d'origine |
| Édition en vis-à-vis | Comparaison source/traduction paragraphe par paragraphe en deux volets : modification, approbation ou rejet avec annotations |
| Base de connaissances / mémoire de traduction (KB/TM) | Terminologie d'entreprise, paires bilingues et traductions fixes des noms de marque entrent dans la base de connaissances et sont appliquées automatiquement lors de la traduction |

Principaux atouts :

- **Interopérabilité entre plus de 40 langues** ; l'interface elle-même est disponible en 12 langues (简体中文, 繁體中文, English, 日本語, 한국어, Deutsch, Français, Español, Português, Русский, العربية, ไทย), l'arabe étant rendu en disposition de droite à gauche.
- **Deux modes de traduction** : « Rapide », léger et efficace ; « Relecture professionnelle », avec calibration terminologique via la base de connaissances, évaluation qualité et seuils de validation stricts — adapté aux contenus destinés à la publication.
- **Isolation entreprise** : les données, les bases de connaissances et les membres de chaque tenant sont isolés ; au sein d'une organisation, les bases de connaissances et les budgets sont en outre isolés par département.
- **Cohérence multi-plateformes** : outre l'application web, une extension de traduction à la sélection pour navigateur, un complément Word, une extension VS Code et une API/SDK ouverte offrent les mêmes traductions et la même terminologie que l'interface web.

---

## 2. Premiers pas

### 2.1 Connexion

Ouvrez l'adresse d'accès fournie par votre entreprise (adresse de la plateforme ou sous-domaine dédié de l'entreprise) et saisissez votre **nom d'utilisateur** et votre **mot de passe** sur la page de connexion.

- Mot de passe oublié : cliquez sur « Mot de passe oublié ? » et réinitialisez-le à l'aide d'un code de vérification reçu par l'adresse e-mail liée.
- Si votre entreprise a activé l'identité unifiée (SSO), la page de connexion affiche le bouton « Ou connectez-vous avec votre identité d'entreprise » ; un clic suffit pour se connecter avec le compte d'entreprise.
- Première connexion : si l'administrateur ayant créé votre compte n'a pas renseigné d'adresse e-mail, une fenêtre non fermentable « Associez votre adresse e-mail » s'affiche — terminez d'abord l'association. Pour les comptes créés en masse, le changement de mot de passe est imposé à la première connexion (« Premier accès : veuillez modifier votre mot de passe »).

### 2.2 Inscription / rejoindre une entreprise

Deux chemins d'accès :

- **Rejoindre une entreprise existante** : lors de l'inscription, saisissez le code d'organisation + un **code d'invitation** (obligatoire — demandez-le à l'administrateur de votre entreprise) pour rejoindre le département correspondant en tant qu'utilisateur ordinaire. En accédant à la page d'inscription via le sous-domaine dédié de l'entreprise (`code.domain`), les informations de l'entreprise sont préremplies ; cette entrée ne permet de s'inscrire que comme membre ordinaire de cette entreprise.
- **Créer une nouvelle entreprise** : laissez le code d'invitation vide pour créer une nouvelle organisation — vous en devenez l'administrateur d'organisation et pouvez ensuite inviter des collègues (voir chapitre 9).

L'inscription peut être guidée par l'assistant IA : choix du type de compte (personnel/entreprise), du rôle dans l'entreprise (l'administrateur crée une entreprise / le membre rejoint via un code d'invitation), du secteur et du rôle professionnel, puis saisie des informations du compte — suivez les étapes ; vous pouvez revenir en arrière à tout moment. Choisir « Personnel » ouvre une **version personnelle** ; les limites fonctionnelles sont décrites en [3.1 Utilisateurs de la version personnelle](#31-utilisateurs-de-la-version-personnelle).

### 2.3 Découvrir l'interface

Après la connexion, vous arrivez dans l'atelier ; les principales zones :

| Zone | Contenu |
|------|------|
| Barre supérieure | Trois onglets d'atelier : **Traduction instantanée**, **Tickets de traduction**, **Édition en vis-à-vis** ; à droite, le badge de solde de points, la cloche de notifications, le sélecteur de langue et le menu du compte ; les administrateurs de département et au-dessus voient en plus le bouton « Téléverser une base de connaissances » et l'entrée « Console d'administration » |
| Tiroir « Plus » | Accès aux pages autonomes (Mon compte, etc.) et pied de page |
| Zone principale | L'espace de travail de l'onglet actif |
| « Assistant IA » (en bas à droite) | Widget d'assistant intelligent disponible à tout moment (voir chapitre 11) |

- **Changer la langue de l'interface** : le menu déroulant de langue dans la barre supérieure liste chaque langue dans sa propre écriture (par ex. « 日本語 », « 한국어 ») ; le choix s'applique immédiatement à tout le site. Lors de la première visite, la langue du navigateur est détectée automatiquement.
- **Mobile** : une utilisation depuis le navigateur d'un téléphone ou d'une tablette est directement possible ; sur écran étroit, la barre latérale de la console d'administration se convertit automatiquement en menu tiroir.
- **Menu du compte** (avatar en haut à droite) : accès à la console d'administration (administrateur de département et au-dessus) / retour à l'atelier, changer le mot de passe, changer de courriel, Mon rôle professionnel, déconnexion.

### 2.4 Effectuer votre première traduction

1. Collez le texte à traduire dans la zone de saisie de « Traduction instantanée » ;
2. Confirmez la langue source (« Détection automatique » par défaut) et choisissez une ou plusieurs langues dans le sélecteur de langue cible (les candidatures sont groupées en « Langues de la base de connaissances / Autres langues ») ;
3. Choisissez le mode (Rapide / Relecture professionnelle) et cliquez sur « Traduire » ;
4. La traduction s'affiche segment par segment dans des bulles de conversation — copiez la traduction, consultez la source ou poursuivez avec des instructions de style.

Pour la traduction de fichiers, voir le chapitre 5.

---

## 3. Rôles et permissions

Trois niveaux de rôles au sein du tenant, aux permissions croissantes :

| Rôle | Qui | Fonctions visibles |
|------|--------|----------|
| **Utilisateur ordinaire** | Tous les membres | Traduction instantanée, tickets de traduction (créer/consulter/télécharger), édition en vis-à-vis, centre de notifications, assistant IA, Mon compte ; langue de l'interface et rôle professionnel |
| **Administrateur de département** | Désigné par un administrateur d'organisation | Toutes les fonctions de l'utilisateur ordinaire + les menus de base de la console d'administration : **Vue d'ensemble** (avec le détail d'utilisation), **Retours / approbation** (approuver les tickets, répondre aux retours), **Base de connaissances** (packs du département/packs interdépartementaux) et le bouton « Téléverser une base de connaissances » de la barre supérieure |
| **Administrateur d'organisation** | Au moins un par entreprise (créateur ou personne mandatée) | Toutes les fonctions de l'administrateur de département + **Organisation et membres** (arborescence des départements, membres, codes d'invitation, budgets), **Appels externes** (clés API, webhooks, SDK), **Base de connaissances** pour tous les types de packs (organisation/département/interdépartemental), la maintenance des noms de marque (traductions fixes) et la configuration de la synchronisation SCIM |

Précisions :

- Ce guide ne couvre que les rôles internes au tenant ci-dessus ; les fonctions d'exploitation de la plateforme sont hors périmètre.
- Les actions libellées « revue mémoire » ou « archiver le retour » sur l'écran « Retours / approbation » relèvent des processus de la plateforme ; elles n'apparaissent pas dans l'interface du tenant et ne requièrent aucune attention.
- Pour toute question sur les permissions, contactez l'administrateur d'organisation de votre entreprise.

### 3.1 Utilisateurs de la version personnelle

Choisir le type de compte « Personnel » à l'inscription (ni code d'organisation ni code d'invitation requis) ouvre une **version personnelle** — le badge d'identité de la barre supérieure affiche « Version personnelle » ; le compte est géré uniquement par vous, sans notion de membres ni de départements d'entreprise.

Fonctions disponibles en version personnelle :

| Fonction | Description |
|------|------|
| Les trois ateliers | Traduction instantanée, Tickets de traduction, Édition en vis-à-vis — identiques à l'expérience entreprise |
| Console d'administration | Accessible depuis le menu du compte : **Base de connaissances** (téléverser et maintenir votre propre glossaire, visible et utilisable uniquement par vous), **Appels externes** (délivrance autonome de clés API — pour l'extension navigateur, le complément Word et les SDK), **Vue d'ensemble** (votre détail d'utilisation) |
| Mon rôle professionnel | Comme en entreprise — un pack de rôle préconfiguré est monté selon votre fonction (voir 7.5) |
| Pages autonomes | Plus → Mon compte ; changer le mot de passe, changer de courriel, centre de notifications, suppression du compte |

Différences avec la version entreprise :

- **Pas d'« Organisation et membres »** : pas de départements, d'ouverture de comptes membres ni de budgets départementaux ;
- **Pas d'étape d'approbation pour les tickets** : un ticket de traduction créé passe directement en file d'exécution et ne reste jamais en « En attente d'approbation » ;
- Pas de sous-domaine d'entreprise dédié ni de base de connaissances partagée de l'organisation ; les packs sectoriels et les packs linguistiques et culturels de la plateforme s'appliquent normalement de façon automatique.

En cas de quota ou de solde insuffisant en version personnelle, suivez simplement l'invite affichée à l'écran.

---

## 4. Traduction instantanée (atelier)

« Traduction instantanée » est la page par défaut après la connexion : une conversation plein écran, l'historique en haut et la zone de saisie épinglée en bas.

### 4.1 Zone de saisie (barre d'outils du pied de la zone)

Une carte de saisie contenant une ligne de saisie auto-extensible et une barre d'outils sur une ligne :

| Contrôle | Description |
|------|------|
| Source · Détection automatique | Choisir la langue source ; la détection automatique est activée par défaut |
| Langue cible | Ouvre le sélecteur ; les langues sont groupées en « Langues de la base de connaissances / Autres langues (traduction IA) », et « Autres langues… » permet de rechercher parmi toutes les langues ; les langues choisies s'affichent en pastilles — au-delà de 3, elles se replient en « +n » ; déroulez pour les retirer une par une |
| Relecture professionnelle / Rapide | Mode de traduction. Rapide = premier jet IA + relecture, léger et à haut débit ; Relecture professionnelle = calibration terminologique par la base de connaissances + évaluation qualité + seuils de validation stricts, pour les contenus destinés à la publication |
| Traduction condensée | Coché, la traduction est contrainte en longueur — adapté aux titres, textes de boutons et autres cas à limite de caractères |
| Traduire (bouton principal) | Lance la traduction ; pendant la génération, « Arrêter la génération » interrompt |

La consommation estimée s'affiche avant l'envoi. En cas de solde insuffisant ou de quota épuisé, l'interface vous en informe et précise de contacter votre administrateur.

### 4.2 Conversation et résultats

- La source et la traduction apparaissent l'une au-dessus de l'autre dans la même bulle ; la traduction s'affiche segment par segment en flux.
- Chaque traduction porte l'indication de son origine : **correspondance exacte / correspondance floue / similarité sémantique** (issues de la base de connaissances) ou **traduction en ligne** (générée par l'IA) ; les termes de la base de connaissances touchés sont surlignés.
- Actions sur la bulle : copier la traduction, voir le texte source ; continuez à envoyer des instructions pour ajuster le ton ou le style — le contexte se poursuit.
- Les étapes du processus de traduction (recherche terminologique → traduction automatique → calibration terminologique → traduction terminée) sont annoncées à l'écran par des indications de progression.

### 4.3 Gestion de la session

L'en-tête de la conversation propose :

- **Rechercher dans cette session** (raccourci Ctrl/⌘+K) ;
- **Exporter l'historique de session** (fichier Markdown) ;
- **Vider la conversation**.

### 4.4 Affichage de la consommation

Les consommations affichées à l'écran s'expriment uniformément en « points », convertis en « nombre approximatif de phrases ». Près de la zone de saisie sont affichés en permanence : le solde (points ≈ phrases), la consommation du jour et la progression du budget du département (si vous êtes rattaché à un département doté d'un budget).

---

## 5. Tickets de traduction (traduction de documents)

Toute **traduction de fichiers** se lance depuis la page « Tickets de traduction » et suit le flux complet : création → (selon la configuration de l'entreprise) approbation → traduction → livraison.

### 5.1 Créer un ticket

Cliquez sur « Créer une tâche de traduction » et choisissez l'onglet « Texte » ou « Fichier » :

- **Texte** : collez un texte long, choisissez les langues cibles et le mode, puis créez le ticket directement.
- **Fichier** : téléversez un ou plusieurs fichiers ; plusieurs langues cibles peuvent être cochées d'un coup (les livrables sont regroupés dans un zip à télécharger).

Champs principaux :

| Champ | Description |
|------|------|
| Mode de livraison | **Mise en forme conservée** (par défaut) : livraison du fichier traduit dans le format d'origine (mise en forme restituée), avec en complément un livrable de texte seul .md ; **Texte seul** : extraction locale du corps du texte, traduction et livraison en .md sans garantie de mise en forme (adapté aux anciens formats doc/ppt/xls, odt/epub/rtf, etc.) |
| Mode | Rapide (sans base de connaissances) / Relecture professionnelle |
| Langues cibles | Choix multiple possible |
| Consommation estimée | Avant la création : « Coût estimé {n} phrases · solde {n} » |

Formats pris en charge :

| Catégorie | Formats | Livraison |
|------|------|------|
| Mise en forme restituée | docx, pptx, xlsx, pdf, txt, csv, md | Fichier traduit dans le même format |
| Livrable en tableau comparatif | srt, vtt, json, yaml, etc. | xlsx source/traduction en vis-à-vis |
| Texte seul uniquement | anciens formats doc, ppt, xls, odt/ods/odp, rtf, epub | Traduction en .md |

Limites et précisions :

- Le nombre et la taille des fichiers par ticket sont plafonnés ; un PDF de plus de 40 Mo ou de plus de 120 pages est refusé de manière conviviale, avec le conseil de le convertir d'abord en docx.
- Les PDF scannés (images pures) ne sont pas pris en charge ; le texte incéré dans les images n'est pas traduit, par conception du produit.
- En cas d'échec de la réécriture de la mise en forme, le fichier concerné est automatiquement dégradé en livraison .md et une notification interne vous en informe — le ticket dans son ensemble n'échoue pas pour autant.

### 5.2 États des tickets et cycle de vie

La liste « Mes tâches » est filtrable par état ; signification des états :

```
En file d'attente → Traduction en cours → Terminé
En attente d'approbation → Approuvé → Traduction en cours …   (lorsque l'approbation est activée par l'entreprise)
                  └→ Rejeté
Tout état en cours → Annulé
```

- Un ticket en cours peut être « Annulé » ; un ticket terminé peut être « Supprimé ».
- Si votre entreprise a activé l'approbation, les tickets atterrissent d'abord en « En attente d'approbation » ; un administrateur de département ou d'organisation les approuve (Approuvé) ou les rejette (Rejeté, avec motif) depuis la console d'approbation de l'écran « Retours / approbation » de la console d'administration.

### 5.3 Suivi de la progression et téléchargement

Ouvrez le ticket pour voir le fil d'étapes « Progression » : extraction, correspondance base de connaissances, premier jet IA, relecture professionnelle, validation stricte, contrôle culturel, réécriture du fichier, etc. (les étapes affichées dépendent du mode et du type de fichier).

- Une fois terminé, cliquez sur « Télécharger » pour récupérer la traduction ; le repli en texte seul reste disponible via « Télécharger le texte traduit (.md) ».
- Pour les tâches multi-langues / multi-fichiers, le téléchargement est un paquet zip.

### 5.4 Rapport de qualité

Les tickets terminés en mode Relecture professionnelle comportent un bloc « Rapport de qualité » : conclusion générale (contrôle qualité réussi / contrôle qualité échoué / contrôle qualité douteux), score d'évaluation automatique (sur 100) et commentaires par segment. En cas de désaccord avec un résultat, utilisez « Soumettre un retour » sur la fiche du ticket ; l'administrateur le verra et y répondra dans la console d'administration.

### 5.5 Passerelle vers l'édition en vis-à-vis

Depuis un ticket terminé, ouvrez « Édition en vis-à-vis » en un clic pour une relecture segment par segment — voir le chapitre suivant.

---

## 6. Édition en vis-à-vis

« Édition en vis-à-vis » sert à la relecture humaine finale des traductions de tickets :

1. Accédez-y depuis la fiche du ticket, ou « Chargez » un ticket par ID/numéro de ticket sur la page « Édition en vis-à-vis » ;
2. Source dans le volet gauche, traduction dans le volet droit, segment par segment, avec surlignage des termes de la base de connaissances du côté source ;
3. Pour chaque segment : **Approuver** / **Rejeter** (avec annotation/motif de rejet) / modifier directement la traduction ;
4. Cliquez sur « Enregistrer » pour conserver les révisions humaines.

Les paires validées par un humain et de haute qualité sont une source précieuse pour la mémoire de traduction — les administrateurs de la base de connaissances de l'entreprise peuvent les organiser et les importer (voir chapitre 7).

---

## 7. Base de connaissances et mémoire de traduction

La base de connaissances (KB) fait que la traduction « connaît votre entreprise » : terminologie unifiée, réutilisation des traductions confirmées, formulations de conformité verrouillées.

### 7.1 Quatre niveaux de contenu

| Niveau | Contenu | Usage typique |
|----|------|----------|
| L1 Termes | Paires terminologiques (y compris traductions fixes des noms de marque et de produit) | Unification contraignante du vocabulaire |
| L2 Mémoire de traduction (TM) | Paires de phrases source/traduction confirmées | Réutilisation de phrases entières ou de fragments |
| L3 Expressions de sécurité | Termes interdits / paires de remplacement / normes de style | Validation culturelle stricte : blocage ou remplacement automatique dans la traduction |
| L4 Fragments | Expressions éparses, références de tournures | Exemples de référence pour la correspondance floue |

Priorité de correspondance lors de la traduction : packs du département sur la chaîne de votre organisation (votre département → parents → racine, le plus proche l'emporte) > pack de l'organisation > packs sectoriels / linguistiques et culturels ; les packs interdépartementaux partagés n'interviennent comme niveau de repli qu'après activation par un administrateur.

### 7.2 Qui peut maintenir quoi

| Opération | Rôle requis |
|------|----------|
| « Téléverser une base de connaissances » dans la barre supérieure (reconnaissance du fichier → choix du pack → import) | Administrateur de département et au-dessus |
| Créer/maintenir des **packs de département** et des **packs interdépartementaux** | Administrateur de département et au-dessus |
| Créer/maintenir le **pack de l'organisation (cette entreprise)**, l'interrupteur général de partage interdépartemental et les autorisations par pack | Administrateur d'organisation |
| Maintenir les « noms de marque » (traductions fixes des marques/produits) | Tout rôle ayant accès au panneau Base de connaissances |
| Importer des corpus bilingues / TMX, exporter en TMX | Administrateur de département et au-dessus |
| Maintenir les « Règles culturelles par langue (expressions de sécurité) » | Administrateur de département et au-dessus |

### 7.3 Types de packs

- **Pack de l'organisation** : valable pour toute l'entreprise ;
- **Pack du département** : visible uniquement dans le département et la chaîne de l'organisation — isolation terminologique au niveau départemental ;
- **Pack interdépartemental (partagé)** : non prêté par défaut ; l'interrupteur « Interdépartemental : activé/désactivé » au niveau du pack, combiné à l'interrupteur de stratégie de l'entreprise, détermine s'il s'applique aux autres départements.

### 7.4 Modes d'import

Depuis le panneau « Base de connaissances » de la console d'administration (administrateur de département et au-dessus) :

- **Import de fichiers** : téléversez un glossaire/un document ; après reconnaissance, le système l'écrit dans le pack choisi ;
- **Import de corpus bilingue / TMX** : écriture en masse dans la mémoire de traduction, compatible avec les TMX exportés par les outils CAT du marché (Trados/memoQ, etc.) ;
- **Export TMX** : ramenez la mémoire de l'entreprise dans votre chaîne d'outils existante.

La plateforme monte en outre automatiquement des « packs sectoriels / packs linguistiques et culturels » génériques selon le secteur — aucune maintenance entreprise requise ; ils s'appliquent automatiquement lors de la traduction.

### 7.5 Mon rôle professionnel (tous)

Menu du compte → « Mon rôle professionnel » : choisissez votre fonction dans le dictionnaire de rôles préconfigurés (plus de 15 métiers). Lors de la traduction, le système monte automatiquement le pack de rôle correspondant, pour adapter la terminologie et le ton à votre contexte de travail.

---

## 8. Compte et paramètres personnels

Accès : menu du compte en haut à droite de la barre supérieure, et pages autonomes du tiroir « Plus ».

| Élément | Chemin | Description |
|------|------|------|
| Mon compte | Plus → Mon compte | Consulter nom d'utilisateur/courriel/rôle/tenant ; les administrateurs d'organisation et au-dessus y voient aussi la carte de configuration **Approvisionnement SCIM 2.0 (IdP d'entreprise)** |
| Changer le mot de passe | Menu du compte → Changer le mot de passe | Peut être imposé à la première connexion |
| Changer de courriel | Menu du compte → Changer de courriel | Sert à la récupération du mot de passe et à la réception des notifications |
| Mon rôle professionnel | Menu du compte | Voir 7.5 |
| Notifications | Cloche de la barre supérieure | Messages internes : état des tickets, résultats d'approbation, rappels de livraison dégradée, etc. ; marquage « lu » unitaire ou global |
| Supprimer le compte | Menu du compte (utilisateur ordinaire) | Suppression autonome, effective après confirmation selon l'invite |
| Langue de l'interface | Menu déroulant de langue de la barre supérieure | 12 langues, effet immédiat |

---

## 9. Gestion de l'organisation et des membres (administrateur d'organisation)

Accès : menu du compte → « Console d'administration » → « Organisation et membres » dans la barre latérale gauche.

### 9.1 Structure de l'organisation

- **Nouvelle organisation / nouveau département** : maintenez l'arborescence multi-niveaux de l'entreprise ; renommage pris en charge.
- **Budget du département** : « Attribuer un budget mensuel » par département pour encadrer la consommation de traduction des membres de ce département ; la progression du budget départemental s'affiche dans l'interface des membres.

### 9.2 Comptes des membres

La page « Comptes de membres » gère tous les comptes de l'entreprise :

- **Ajouter un utilisateur** : créez un membre en désignant son rôle (Utilisateur ordinaire / Administrateur de département / Administrateur d'organisation) et son rattachement d'organisation ;
- **Import en masse des utilisateurs (Excel)** : ouvrez plusieurs comptes de membres d'un coup ; les mots de passe initiaux sont notifiés par e-mail et les membres doivent changer leur mot de passe à la première connexion ;
- Pour les membres existants : **réinitialiser le mot de passe**, **activer/désactiver** le compte, ajuster le rôle et le rattachement d'organisation.

### 9.3 Inviter des collaborateurs

La page « Inviter des collaborateurs » génère des **codes d'invitation** (plusieurs possibles, chacun rattachable à un niveau de l'arborescence). Communiquez à vos collègues le code d'organisation + le code d'invitation ; saisis sur la page d'inscription, ils les font rejoindre le département correspondant.

### 9.4 Synchronisation SCIM de l'organisation (facultatif)

La carte SCIM dans « Plus → Mon compte » : si votre entreprise utilise une source d'identité comme Okta ou Entra ID, récupérez le point de terminaison SCIM et le jeton pour automatiser l'approvisionnement/la synchronisation des membres ; une fois activé, les collaborateurs peuvent se connecter sans mot de passe via le bouton SSO de la page de connexion.

---

## 10. Appels externes et intégrations (administrateur d'organisation)

Accès : console d'administration → « Appels externes ». Trois sous-pages : **API ouverte**, **Webhooks**, **SDK officiels**.

### 10.1 Clés API ouvertes

- **Délivrer une clé API** : en clair, elle n'est affichée **qu'une seule fois** à la création — enregistrez-la immédiatement ;
- Les clés prennent en charge **activation/désactivation, rotation, plafonds quotidiens et suppression** ;
- Une clé est rattachée au tenant de votre entreprise et à son solde de points ; les consommations par API sont débitées du même compte que l'interface web.

L'API ouverte fournit : tâches de traduction asynchrones (création → interrogation → téléchargement, multi-fichiers et multi-langues, livrables regroupables en zip), traduction synchrone de textes courts (≤ 5 000 caractères, ≤ 5 langues), consultation du solde et de l'utilisation, et statistiques de la base de connaissances. La documentation des interfaces est consultable sur le site, à l'adresse `/openapi/docs`.

### 10.2 Webhooks de rappel

Enregistrez une URL de rappel et abonnez-vous aux événements (par ex. `translation.completed`) : à la fin d'une traduction, la plateforme notifie activement votre système ; vérification de signature (X-Signature), envoi de test, historique des distributions et nouvelles tentatives sont pris en charge.

### 10.3 SDK et compléments officiels

La page « SDK officiels » fournit les guides d'installation et les accès de téléchargement pour chaque plateforme :

| Plateforme | Obtention | Usage |
|----|----------|------|
| Python | `pip install langcross-translator` | Traduction programmatique, tâches par lots |
| TypeScript | `npm i @langcross/translator-sdk` | Intégration Node/frontend |
| Java | Distribution par sources Maven | Intégration aux systèmes d'entreprise |
| Extension de traduction à la sélection pour navigateur | Téléchargement zip sur cette page (aucun canal de boutique d'applications) | Sélectionner un texte sur n'importe quelle page web → cliquer le bouton flottant « 译 » → consulter la traduction dans une bulle et la copier ; raccourci Alt+Shift+T (Alt+T sur macOS) |
| Complément Word | Manifeste de complément fourni par le site ; dans Word, « Insertion → Get Add-ins → Upload My Add-in » pour un chargement latéral | Traduire le texte sélectionné dans Word et réinsérer le résultat dans le document |
| Extension VS Code | vscode-extension dans le dépôt | Traduction sur place de la sélection dans l'éditeur, recherche terminologique |

**Configuration de l'extension/complément** (même logique dans la popup de l'extension de sélection et le volet Word) : renseignez l'**adresse du service** (URL racine du site), la **clé API** (délivrée au 10.1), les **langues cibles** et le **mode de traduction** (Rapide/Relecture professionnelle) pour l'utiliser ; la terminologie et la mémoire de traduction sont identiques à celles de l'interface web.

⚠ Après une mise à niveau de l'extension, si le style semble inchangé : remplacez le contenu du **même répertoire** par le nouveau zip, puis cliquez sur le bouton **Actualiser** de la carte de l'extension dans `chrome://extensions`.

### 10.4 Limites de l'administrateur de département

Si un administrateur de département a besoin d'une intégration API, qu'il demande à l'administrateur d'organisation de délivrer une clé ; le menu « Appels externes » ne s'affiche pas pour les administrateurs de département.

---

## 11. Notifications, retours et assistant IA

### 11.1 Centre de notifications (tous)

Cloche de la barre supérieure : les non-lectures sont interrogées toutes les 30 secondes ; le menu déroulant affiche les messages internes (tickets terminés, résultats d'approbation, rappels de dégradation du mode de livraison, etc.), avec marquage « lu » unitaire ou global.

### 11.2 Soumettre et traiter des retours

- **Tous** : sur un ticket terminé, « Soumettre un retour » depuis la fiche, en décrivant les segments problématiques et vos attentes.
- **Administrateurs de département/d'organisation** : « Retours / approbation » de la console d'administration liste les retours du tenant et permet d'**y répondre** ; sur le même écran, la « console d'approbation » traite les tickets en attente (Approuver/Rejeter).
- Panneau « Vue d'ensemble » (administrateur de département et au-dessus) : tableau de bord de l'entreprise et détail d'utilisation (export CSV disponible).

### 11.3 Widget Assistant IA (tous, visiteurs compris)

Le bouton « Assistant IA » en bas à droite ouvre une conversation à tout moment : questions sur le produit, guidage des fonctions, accès rapides. L'échange n'est pas perdu après rechargement de la page (cache local → restauration automatique de l'historique côté serveur).

---

## 12. Questions fréquentes

**Q1 : Le bouton « Traduire » ne réagit pas / message de quota insuffisant ?**
Le solde actuel ou le budget du département est épuisé. Les utilisateurs ordinaires doivent contacter l'administrateur de leur entreprise ; les administrateurs peuvent consulter le détail d'utilisation dans la console d'administration.

**Q2 : Où lancer une traduction de fichier ? Pourquoi la zone de chat n'accepte plus les pièces jointes ?**
La traduction de fichiers passe désormais uniquement par « Tickets de traduction → Fichier » ; la conversation ne fait que de la traduction de texte.

**Q3 : Mon PDF a été refusé à la traduction ?**
Un PDF de plus de 40 Mo ou de plus de 120 pages doit d'abord être converti en docx avant création du ticket ; les PDF scannés (images seules) ne sont pas pris en charge pour l'instant.

**Q4 : Pourquoi la traduction ne correspond-elle pas à mon glossaire ?**
Vérifiez que les termes ont bien été importés dans la base de connaissances et que le pack est visible par votre département (chapitre 7) ; pour des contenus destinés à la publication, choisissez le mode « Relecture professionnelle ».

**Q5 : La mise en forme du docx livré diffère de l'original ?**
Le mode « Mise en forme conservée » préserve la mise en forme dans la grande majorité des cas ; sur certaines mises en page complexes, la livraison est dégradée en texte .md avec notification interne. Téléchargez le .md puis relecturez dans l'édition en vis-à-vis, ou soumettez un retour.

**Q6 : Je viens de traduire, mais le solde n'a pas bougé ?**
La mesure est enregistrée de façon synchrone à l'instant de la fin de la traduction ; si la page ne s'est pas rafraîchie, rechargez-la une fois ; en cas d'anomalie persistante, soumettez un retour.

**Q7 : Puis-je l'utiliser sur téléphone ou tablette ?**
Oui, en accédant directement depuis le navigateur ; l'interface s'adapte aux écrans étroits ; extensions et compléments sont orientés bureau.

**Q8 : Du texte est tronqué ou le rendu est bizarre dans certaines langues (arabe, etc.) ?**
Basculez d'abord vers une autre langue pour confirmer qu'il s'agit d'une seule chaîne, puis soumettez un retour avec une capture d'écran par le canal de retour.

---

## 13. Annexe

### A. Référence rapide des noms d'interface (简体中文 / Français)

| 简体中文 | Français |
|----------|---------|
| 即时翻译 | Traduction instantanée |
| 翻译工单 | Tickets de traduction |
| 对照编辑 | Édition en vis-à-vis |
| 更多 | Plus |
| 我的账号 | Mon compte |
| 上传知识库 | Téléverser une base de connaissances |
| 进入管理后台 | Console d'administration |
| 通知中心 | Notifications |
| 专业校对 / 快速 | Relecture professionnelle / Rapide |
| 缩翻 | Traduction condensée |
| 自动检测 | Détection automatique |
| 知识库语言 / 其他语言（AI翻译） | Langues de la base de connaissances / Autres langues (traduction IA) |
| 创建翻译工单 | Créer une tâche de traduction |
| 交付方式：还原文件模式 / 纯文案模式 | Mode de livraison : Mise en forme conservée / Texte seul |
| 我的工单 | Mes tâches |
| 排队中 / 翻译中 / 待审批 / 已批准 / 已驳回 / 已完成 / 已取消 | En file d'attente / Traduction en cours / En attente d'approbation / Approuvé / Rejeté / Terminé / Annulé |
| 执行进度 | Progression |
| 质检报告 | Rapport de qualité |
| 审批台 / 批准 / 驳回 | Console d'approbation / Approuver / Rejeter |
| 知识库 | Base de connaissances |
| 组织包（本企业）/ 部门包（本部门）/ 跨部门包（共享） | Pack de l'organisation / Pack du département / Pack interdépartemental (partagé) |
| 文件导入 / 双语语料 · TMX 导入 | Import de fichiers / Import de corpus bilingue / TMX |
| 语言文化规范（安全句） | Règles culturelles par langue (expressions de sécurité) |
| 组织与成员 | Organisation et membres |
| 组织结构 / 邀请员工 / 成员账户 | Organisation / Inviter des collaborateurs / Comptes de membres |
| 部门预算 | Budget du département |
| 开通用户 / Excel 批量导入用户 | Ajouter un utilisateur / Import en masse des utilisateurs (Excel) |
| 邀请码管理 / 生成邀请码 | Codes d'invitation / Générer un code |
| 普通用户 / 部门管理员 / 组织管理员 | Utilisateur / Administrateur de département / Administrateur d'organisation |
| 外部调用 / 开放 API / 回调通知 / 官方 SDK | Appels externes / API ouverte / Webhooks / SDK officiels |
| 我的职业角色 | Mon rôle professionnel |
| 修改密码 / 修改邮箱 / 注销账号 | Changer le mot de passe / Changer de courriel / Supprimer le compte |
| 积分 | points |

### B. Langues d'interface prises en charge (12)

简体中文 · 繁體中文 · English · 日本語 · 한국어 · Deutsch · Français · Español · Português · Русский · العربية (disposition RTL) · ไทย

### C. Récapitulatif des raccourcis clavier

| Raccourci | Fonction |
|--------|------|
| Ctrl/⌘ + K | Traduction instantanée : rechercher dans cette session |
| Alt+Shift+T (macOS : Alt+T) | Extension de sélection du navigateur : traduire la sélection |
| Cmd/Ctrl+Alt+T | Extension VS Code : traduire la sélection sur place |

### D. Formats pris en charge pour la traduction de fichiers

Voir le tableau du [5.1 Formats pris en charge](#51-créer-un-ticket).

---

*Ce guide décrit les fonctions produit disponibles dans le tenant. Pour les questions commerciales ou de facturation, contactez l'administrateur de votre entreprise ou l'équipe de service LangCross.*
