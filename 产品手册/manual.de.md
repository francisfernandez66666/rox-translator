# LangCross（能言） · Benutzerhandbuch

> Zielgruppe: Nutzer des Privattarifs sowie alle Mitglieder eines Unternehmens-/Team-Mandanten (gewöhnliche Nutzer, Abteilungsadministratoren, Organisationsadministratoren)
> Umfang: ausschließlich Funktionen, die innerhalb des Mandanten sichtbar und nutzbar sind. Funktionen der Plattform-Administration sowie Pläne, Aufwertungen, Rechnungen und Aktionskampagnen sind bewusst nicht enthalten.
> Die UI-Bezeichnungen folgen dem tatsächlichen Producttext; bei Abweichungen gilt stets die Oberfläche.

---

## Inhaltsverzeichnis

1. [Produktübersicht](#1-produktübersicht)
2. [Erste Schritte](#2-erste-schritte)
3. [Rollen und Berechtigungen](#3-rollen-und-berechtigungen)
4. [Sofortübersetzung (Workspace)](#4-sofortübersetzung-workspace)
5. [Übersetzungsaufträge (Dokumentübersetzung)](#5-übersetzungsaufträge-dokumentübersetzung)
6. [Paralleleditor](#6-paralleleditor)
7. [Wissensdatenbank und Übersetzungsspeicher](#7-wissensdatenbank-und-übersetzungsspeicher)
8. [Konto und persönliche Einstellungen](#8-konto-und-persönliche-einstellungen)
9. [Organisations- und Mitgliederverwaltung (Organisationsadministrator)](#9-organisations--und-mitgliederverwaltung-organisationsadministrator)
10. [Externe Aufrufe und Integrationen (Organisationsadministrator)](#10-externe-aufrufe-und-integrationen-organisationsadministrator)
11. [Benachrichtigungen, Feedback und KI-Assistent](#11-benachrichtigungen-feedback-und-ki-assistent)
12. [Häufige Fragen](#12-häufige-fragen)
13. [Anhang](#13-anhang)

---

## 1. Produktübersicht

**LangCross（能言）** ist eine KI-Übersetzungs-Kollaborationsplattform für Unternehmen und Teams. Sie vereint drei Übersetzungsarten mit einem unternehmensweiten Sprachasset-System:

| Funktion | Beschreibung |
|------|------|
| Sofortübersetzung | Textübersetzung im Chat-Stil — eingeben und sofort übersetzen, auf Wunsch in mehrere Zielsprachen gleichzeitig |
| Übersetzungsaufträge | Dateien im Format docx / pptx / xlsx / pdf hochladen und den vollständigen Ablauf „Auftrag erstellen → Freigabe → Übersetzung → Auslieferung“ durchlaufen lassen; die Übersetzung wird layoutgetreu im ursprünglichen Format ausgeliefert |
| Paralleleditor | Ausgangstext/Übersetzung zweispaltig Absatz für Absatz im Vergleich: ändern, freigeben oder ablehnen, jeweils mit Anmerkungen |
| Wissensdatenbank / Übersetzungsspeicher (KB/TM) | Unternehmensbegriffe, zweisprachige Satzpaare und festgelegte Markenübersetzungen liegen in der Wissensdatenbank und werden beim Übersetzen automatisch getroffen und einheitlich angewendet |

Wesentliche Merkmale:

- **40+ Sprachen** wechselseitig; die Oberfläche selbst steht in 12 Sprachen zur Verfügung (简体中文、繁體中文、English、日本語、한국어、Deutsch、Français、Español、Português、Русский、العربية、ไทย), die arabische Oberfläche ist von rechts nach links aufgebaut.
- **Zwei Übersetzungsmodi**: „Schnell“ leichtgewichtig und effizient; „Profi“ mit Terminologieabgleich gegen die Wissensdatenbank, Qualitätseinschätzung und harten Prüf-Gates — geeignet für Veröffentlichungsinhalte.
- **Unternehmensisolierung**: Daten, Wissensdatenbanken und Mitglieder jedes Mandanten sind voneinander getrennt; innerhalb der Organisation sind Wissensdatenbanken und Budgets zusätzlich nach Abteilung getrennt.
- **Konsistent über alle Enden**: Neben dem Web-Frontend gibt es eine Browser-Erweiterung für Textauswahl, ein Word-Add-in, ein VS-Code-Plugin sowie offene API/SDKs — die Übersetzungseinheit stammt aus derselben Quelle wie im Web.

---

## 2. Erste Schritte

### 2.1 Anmeldung

Rufen Sie die von Ihrem Unternehmen zugewiesene Zugangsadresse auf (Plattformadresse oder unternehmenseigene Subdomain) und geben Sie auf der Login-Seite **Benutzername** und **Passwort** ein.

- Passwort vergessen: Klicken Sie auf „Passwort vergessen?“ und setzen Sie es per Bestätigungscode zurück, der an Ihre verknüpfte E-Mail-Adresse gesendet wird.
- Hat Ihr Unternehmen Unified Identity (SSO) aktiviert, erscheint auf der Login-Seite die Schaltfläche „Oder mit Unternehmensidentität anmelden“ — ein Klick genügt für die Anmeldung mit dem Unternehmenskonto.
- Erste Anmeldung: Hat die Administration beim Anlegen Ihres Kontos keine E-Mail hinterlegt, erscheint ein nicht schließbares Dialogfenster „E-Mail-Adresse verknüpfen“; schließen Sie die Verknüpfung zuerst ab. Bei in Stapelverarbeitung angelegten Konten werden Sie bei der ersten Anmeldung aufgefordert, das Passwort zu ändern.

### 2.2 Registrierung / Unternehmen beitreten

Zwei Wege:

- **Bestehendem Unternehmen beitreten**: Geben Sie bei der Registrierung den Organisationscode + einen **Einladungscode** (Pflichtfeld — erhalten Sie von der Administration Ihres Unternehmens) ein; Sie treten damit der zuständigen Abteilung bei und werden gewöhnlicher Nutzer. Rufen Sie die Registrierungsseite über die unternehmenseigene Subdomain (`Unternehmenscode.Domain`) auf, werden die Unternehmensdaten automatisch vorausgefüllt; über diesen Weg können ausschließlich gewöhnliche Mitglieder dieses Unternehmens registriert werden.
- **Neues Unternehmen gründen**: Lassen Sie das Einladungscode-Feld leer, entsteht bei der Registrierung eine neue Organisation — der Registrierende wird deren Organisationsadministrator und kann anschließend Kolleginnen und Kollegen einladen (siehe Kapitel 9).

Der Registrierungsvorgang wird auf Wunsch vom KI-Assistenten geführt: Kontotyp wählen (privat/Unternehmen), Unternehmensrolle (Administrator gründet ein Unternehmen / Mitglied tritt mit Einladungscode bei), Branche und Berufsrolle, Kontodaten ausfüllen — folgen Sie einfach den Hinweisen, ein Zurück ist jederzeit möglich. Die Wahl von „Privat“ eröffnet einen Privattarif — die funktionalen Grenzen finden Sie in [3.1 Nutzer des Privattarifs](#31-nutzer-des-privattarifs).

### 2.3 Die Oberfläche kennenlernen

Nach der Anmeldung landen Sie im Workspace, mit folgenden Bereichen:

| Bereich | Inhalt |
|------|------|
| Kopfleiste | Drei Workspace-Tabs: **Sofortübersetzung**, **Übersetzungsaufträge**, **Paralleleditor**; rechts das Punkteguthaben-Badge, die Benachrichtigungsglocke, die Sprachumschaltung und das Kontomenü; Abteilungsadministratoren und höher sehen zusätzlich die Schaltfläche „Wissensdatenbank hochladen“ und den Einstieg „Verwaltungskonsole“ |
| „Mehr“-Drawer | Einstieg zu den Selfservice-Seiten wie „Mein Konto“ sowie die Fußzeile |
| Hauptbereich | Die Arbeitsfläche des aktiven Tabs |
| „KI-Assistent“ (unten rechts) | Jederzeit aufrufbares Assistenz-Widget (siehe Kapitel 11) |

- **Oberflächensprache wechseln**: Das Sprachmenü in der Kopfleiste; jede Sprache nennt sich in ihrer eigenen Schrift (z. B. „日本語“, „한국어“); die Wahl gilt sofort siteweit. Beim ersten Besuch im Browser wird die Sprache automatisch nach der Systemsprache erkannt.
- **Mobile Nutzung**: Smartphone-/Tablet-Browser werden direkt unterstützt; auf schmalen Bildschirmen wird die Seitenleiste der Verwaltung automatisch zum Drawer-Menü.
- **Kontomenü** (Avatar oben rechts): Verwaltungskonsole aufrufen (Abteilungsadministrator und höher) / zurück zum Workspace, Passwort ändern, E-Mail-Adresse ändern, Meine Berufsrolle, Abmelden.

### 2.4 Die erste Übersetzung

1. Fügen Sie den zu übersetzenden Text in das Eingabefeld von „Sofortübersetzung“ ein;
2. Bestätigen Sie die Quellensprache (Standard: „Automatisch erkennen“) und wählen Sie in der Zielsprachen-Auswahl eine oder mehrere Sprachen (die Kandidaten sind nach „Wissensdatenbanksprachen / Andere Sprachen“ gruppiert);
3. Wählen Sie den Modus (Schnell / Profi) und klicken Sie auf „Übersetzen“;
4. Die Übersetzung erscheint absatzweise nacheinander in Chat-Blasen — Sie können sie kopieren, den Ausgangstext ansehen oder per Folgebefehl den Stil anpassen lassen.

Für Dateiübersetzungen siehe Kapitel 5.

---

## 3. Rollen und Berechtigungen

Innerhalb des Mandanten gibt es drei Rollenebenen mit jeweils erweitertem Rechteumfang:

| Rolle | Wer | Sichtbare Funktionen |
|------|--------|----------|
| **Gewöhnlicher Nutzer** | Alle Mitglieder | Sofortübersetzung, Übersetzungsaufträge (erstellen/ansehen/herunterladen), Paralleleditor, Benachrichtigungszentrum, KI-Assistent, Mein Konto; Oberflächen- und Berufsrolleneinstellungen |
| **Abteilungsadministrator** | Vom Organisationsadministrator benannt | Alle Funktionen eines gewöhnlichen Nutzers plus die Basis-Menüs der Verwaltung: **Übersicht** (inkl. Nutzungsdetails), **Feedback/Freigaben** (Aufträge freigeben, Feedback beantworten), **Wissensdatenbank** (Abteilungs-/abteilungsübergreifende Pakete), „Wissensdatenbank hochladen“ in der Kopfleiste |
| **Organisationsadministrator** | Mindestens einer pro Unternehmen (Gründer oder Beauftragter) | Alle Funktionen eines Abteilungsadministrators plus **Organisation & Mitglieder** (Abteilungsbaum, Mitglieder, Einladungscodes, Budgets), **Externe Aufrufe** (API-Keys, Webhooks, SDKs), **Wissensdatenbank** mit allen Pakettypen (Organisations-/Abteilungs-/abteilungsübergreifende Pakete), Pflege der Markennamen (festgelegte Übersetzungen) sowie die SCIM-Organisationssynchronisation |

Hinweise:

- Dieses Handbuch behandelt ausschließlich die oben genannten Rollen im Mandanten; Funktionen der Plattform-Administration sind nicht enthalten.
- Aktionen auf dem Tab „Feedback/Freigaben“ mit Bezeichnungen wie „Speicherprüfung“ oder „Feedback-Archivierung abschließen“ gehören zu prozessen der Plattformseite, erscheinen in der Mandantenoberfläche nicht und erfordern keine Aufmerksamkeit.
- Bei Fragen zu Berechtigungen wenden Sie sich an den Organisationsadministrator Ihres Unternehmens.

### 3.1 Nutzer des Privattarifs

Wählen Sie bei der Registrierung den Kontotyp „Privat“ (ohne Organisationscode und Einladungscode), wird ein **Privattarif**-Konto eröffnet — das Identitäts-Badge in der Kopfleiste zeigt „Privattarif“. Das Konto verwalten Sie selbst; es gibt keine Unternehmensmitglieder und keine Abteilungen.

Funktionen des Privattarifs:

| Funktion | Beschreibung |
|------|------|
| Die drei Workspaces | Sofortübersetzung, Übersetzungsaufträge, Paralleleditor — vollständig identisch mit der Unternehmensversion |
| Verwaltungskonsole | Über das Kontomenü erreichbar: **Wissensdatenbank** (eigene Glossare hochladen und pflegen, nur für Sie selbst sichtbar und nutzbar), **Externe Aufrufe** (API-Keys im Selfservice ausstellen, für Browser-Erweiterung/Word-Add-in/SDK), **Übersicht** (Ihre eigenen Nutzungsdetails) |
| Meine Berufsrolle | Wie in der Unternehmensversion — das passende Rollenpaket wird je nach Position automatisch zusammengestellt (siehe 7.5) |
| Selfservice-Seiten | Mehr → Mein Konto; Passwort ändern, E-Mail-Adresse ändern, Benachrichtigungszentrum, Konto deaktivieren |

Unterschiede zur Unternehmensversion:

- **Kein „Organisation & Mitglieder“**: Abteilungen, Mitgliederanlage und Abteilungsbudgets bestehen nicht;
- **Kein Freigabeschritt bei Aufträgen**: Ein erstellter Übersetzungsauftrag geht direkt in die Ausführungswarteschlange und bleibt nie im Status „Freigabe ausstehend“ stehen;
- Keine unternehmenseigene Subdomain und keine unternehmensweit geteilte Wissensdatenbank; branchenweite Plattform-Pakete wie Branchen- und Sprachkultur-Pakete greifen weiterhin automatisch.

Wenn Ihr Kontingent oder Guthaben im Privattarif nicht ausreicht, folgen Sie einfach den Hinweisen auf dem Bildschirm.

---

## 4. Sofortübersetzung (Workspace)

„Sofortübersetzung“ ist die Standardseite nach der Anmeldung: ein ganzseitiger Dialog — der Verlauf oben, die Eingabe unten fixiert.

### 4.1 Eingabebereich (Symbolleiste am Kartenfuß)

Eine Eingabekarte mit einemzeilig mitwachsendem Eingabefeld und einer einzeiligen Symbolleiste:

| Steuerelement | Beschreibung |
|------|------|
| Quelle · Automatisch erkennen | Quellensprache wählen; standardmäßig automatische Erkennung |
| Zielsprache | Öffnet die Sprachauswahl; die Sprachen sind nach „Wissensdatenbanksprachen / Andere Sprachen (AI)“ gruppiert, über „Weitere Sprachen…“ lässt sich der vollständige Vorrat durchsuchen; gewählte Sprachen erscheinen als Chips — über 3 werden sie zu „+n“ reduziert, per Klick lassen sie sich einzeln entfernen |
| Profi / Schnell | Übersetzungsmodus. Schnell = KI-Erstübersetzung + Korrektur, leichtgewichtig mit hohem Durchsatz; Profi = vollständiger Ablauf mit Terminologieabgleich gegen die Wissensdatenbank, Qualitätseinschätzung und harten Prüf-Gates — für offizielle Veröffentlichungen |
| Kurzfassung | Mit Aktivierung wird die Übersetzungslänge nach Vorgabe verdichtet — geeignet für Titel, Button-Beschriftungen und andere Szenarien mit Zeichenbudget |
| Übersetzen (Hauptbutton) | Startet die Übersetzung; während der Generierung können Sie „Stopp“ klicken |

Der geschätzte Verbrauch wird vor dem Absenden angezeigt. Bei unzureichendem Guthaben oder ausgeschöpftem Kontingent weist die Oberfläche darauf hin und erläutert, dass die Administration zu kontaktieren ist.

### 4.2 Unterhaltung und Ergebnisse

- Ausgangstext und Übersetzung erscheinen innerhalb derselben Blase untereinander; die Übersetzung wird absatzweise gestreamt.
- Jede Übersetzung trägt ihre Entstehungsart als Kennzeichen: **Exakter Treffer / Unscharfer Treffer / Semantische Ähnlichkeit** (aus der Wissensdatenbank) oder **Online-Übersetzung** (KI-generiert); getroffene Begriffe der Wissensdatenbank werden hervorgehoben.
- Blasen-Aktionen: Übersetzung kopieren, Ausgangstext ansehen; senden Sie weitere Befehle, um Ton oder Stil anpassen zu lassen — der Kontext wird fortgeführt.
- Die Phasen des Übersetzungsprozesses (Begriffssuche → Maschinenübersetzung → Terminologieabgleich → Übersetzung abgeschlossen) werden in der Oberfläche als Statushinweise angezeigt.

### 4.3 Sitzungsverwaltung

Oben im Dialog befinden sich:

- **In dieser Unterhaltung suchen** (Tastenkürzel Strg/⌘+K);
- **Unterhaltung exportieren** (Markdown-Datei);
- **Unterhaltung leeren**.

### 4.4 Nutzungsanzeige

Der Verbrauch wird in der Oberfläche einheitlich in „Punkten“ (credits) angezeigt und auf „entspricht etwa X Sätzen“ umgerechnet. Nahe dem Eingabebereich sind stets sichtbar: Guthaben (Punkte ≈ Satzanzahl), heutiger Verbrauch und — sofern einer Abteilung mit Budget zugeordnet — der Fortschritt des Abteilungsbudgets.

---

## 5. Übersetzungsaufträge (Dokumentübersetzung)

Alle **Dateiübersetzungen** starten auf der Seite „Übersetzungsaufträge“ und durchlaufen den vollständigen Ablauf: Auftrag erstellen → (je nach Unternehmenseinstellung) Freigabe → Übersetzung → Auslieferung.

### 5.1 Auftrag erstellen

Klicken Sie auf „Übersetzungsauftrag erstellen“ und wählen Sie den Tab „Text“ oder „Datei“:

- **Text**: Langtext einfügen, Zielsprachen und Modus wählen und den Auftrag direkt anlegen.
- **Datei**: Eine oder mehrere Dateien hochladen; es können mehrere Zielsprachen gleichzeitig ausgewählt werden (die Ergebnisse werden als ZIP-Archiv zum Download gepackt).

Wichtige Felder:

| Feld | Beschreibung |
|------|------|
| Ausgabemodus | **Format wiederherstellen** (Standard): Auslieferung der Übersetzungsdatei im ursprünglichen Format (Layout-Wiederherstellung), mit zusätzlichem reinem Text-.md-Ausweichprodukt; **Nur Text**: Der Fließtext wird lokal extrahiert und als Übersetzungs-.md ausgeliefert, ohne Layout-Garantie (geeignet für Altformate doc/ppt/xls, odt/epub/rtf usw.) |
| Modus | Schnell (ohne Wissensdatenbank) / Profikorrektorat |
| Zielsprache | Mehrfachauswahl möglich |
| Erwarteter Verbrauch | Vor dem Anlegen wird „Erwarteter Verbrauch {n} Sätze · Guthaben {n} Sätze“ angezeigt |

Unterstützte Dateiformate:

| Kategorie | Formate | Auslieferung |
|------|------|------|
| Layout wiederherstellbar | docx, pptx, xlsx, pdf, txt, csv, md | Übersetzungsdatei im selben Format |
| Gegenüberstellungstabelle | srt, vtt, json, yaml usw. | Ausgangstext/Übersetzung als xlsx-Gegenüberstellung |
| Nur Text | Altformate doc, ppt, xls, odt/ods/odp, rtf, epub | Übersetzung als .md |

Grenzen und Hinweise:

- Die Dateianzahl und -größe pro Auftrag ist begrenzt; PDFs über 40 MB oder 120 Seiten werden mit dem Hinweis abgelehnt, sie zuerst in docx zu konvertieren.
- Gescannte PDFs (nur Bilder) werden nicht unterstützt; Text in Bildern wird gemäß Produktstrategie nicht übersetzt.
- Schlägt das Layout-Backwriting wider Erwarten fehl, wird die betreffende Datei automatisch auf .md-Auslieferung zurückgestuft und per In-App-Nachricht informiert — der Gesamtauftrag scheitert daran nicht.

### 5.2 Auftragsstatus und Ablauf

Die Liste „Meine Aufträge“ lässt sich nach Status filtern; die Bedeutungen:

```
In Warteschlange → Übersetzung läuft → Abgeschlossen
Freigabe ausstehend → Genehmigt → Übersetzung läuft …    (bei aktivierter Freigabe im Unternehmen)
        └→ Abgelehnt
Beliebiger laufender Status → Abgebrochen
```

- Laufende Aufträge können „Abbrechen“; abgeschlossene können „Gelöscht“ werden.
- Hat Ihr Unternehmen die Freigabe aktiviert, landen neue Aufträge zunächst in „Freigabe ausstehend“; ein Abteilungs-/Organisationsadministrator gibt sie auf der Freigabeseite im Verwaltungsmenü „Feedback/Freigaben“ frei (Genehmigt) oder lehnt sie ab (Abgelehnt, mit Begründung).

### 5.3 Fortschritt ansehen und herunterladen

Öffnen Sie den Auftrag, um die Schrittanzeige „Fortschritt“ zu sehen; die Phasen umfassen: Parsen und Extrahieren → Abgleich mit der Wissensdatenbank → KI-Erstübersetzung → Profikorrektorat → Harte Prüfung → Kulturprüfung → Zurückschreiben in die Datei usw. (abhängig von Modus und Dateityp angezeigt).

- Nach Abschluss per „Herunterladen“ die Übersetzung abrufen; der reine Textausweichpfad steht über „Nur Übersetzungstext (.md) herunterladen“ zur Verfügung.
- Bei Mehrsprach-/Mehrdatei-Aufträgen ist der Download unmittelbar das ZIP-Paket.

### 5.4 Qualitätsbericht

Abgeschlossene Aufträge im Profimodus tragen einen Bereich „Qualitätsbericht“: Gesamtergebnis (QA bestanden / QA nicht bestanden / QA-Auffälligkeit), maschinell bewertete Punktzahl (maximum 100) und abschnittsweise Erläuterungen. Bei abweichender Einschätzung können Sie im Auftragsdetail „Feedback übermitteln“ — die Administration sieht und beantwortet dies in der Verwaltung.

### 5.5 Übergang zum Paralleleditor

Abgeschlossene Aufträge lassen sich mit einem Klick im „Paralleleditor“ absatzweise nachprüfen — siehe nächstes Kapitel.

---

## 6. Paralleleditor

Der „Paralleleditor“ dient der menschlichen Endabnahme der Auftragübersetzungen:

1. Vom Auftragsdetail springen Sie direkt hin, oder laden Sie den Auftrag auf der Seite „Paralleleditor“ per Auftrags-ID/Auftragsnummer;
2. Links der Ausgangstext, rechts die Übersetzung, Absatz für Absatz im Vergleich; Begriffe der Wissensdatenbank werden auf der Ausgangstextseite hervorgehoben;
3. Pro Absatz: **Freigeben** / **Ablehnen** (Anmerkung/Ablehnungsgrund eintragen) / die Übersetzung direkt ändern;
4. Mit „Speichern“ die menschlichen Korrekturen festhalten.

Menschlich bestätigte, hochwertige Satzpaare sind eine exzellente Quelle für den Übersetzungsspeicher — die Wissensdatenbank-Administration Ihres Unternehmens kann sie aufbereitet importieren (siehe Kapitel 7).

---

## 7. Wissensdatenbank und Übersetzungsspeicher

Die Wissensdatenbank (KB) lässt Übersetzungen „Ihr Unternehmen verstehen“: einheitliche Terminologie, Wiederverwendung bestätigter Übersetzungen, festgeschriebene Compliance-Formulierungen.

### 7.1 Vier Inhaltsebenen

| Ebene | Inhalt | Typische Verwendung |
|----|------|----------|
| L1 Begriffe | Begriffsgegenüberstellungen (inkl. festgelegter Marken- und Produktnamen) | Verbindlich einheitliche Wortwahl |
| L2 Übersetzungsspeicher (TM) | Bestätigte Ausgangstext/Übersetzungs-Satzpaare | Direkte Wiederverwendung von Sätzen oder Fragmenten |
| L3 Sicherheitsphrasen | Verbotene Wörter / Ersetzungspaare / Stilvorgaben | Harte Sprachkultur-Prüfung: automatisches Blockieren oder Ersetzen in der Übersetzung |
| L4 Fragmente | Vereinzelte Ausdrücke, Satzbaumuster als Referenz | Beispielreferenz für unscharfe Treffer |

Trefferpriorität beim Übersetzen: Pakete Ihrer Organisationskette (eigene Abteilung → übergeordnet → Wurzel, nächste gewinnt) > Organisationspaket > Branchen-/Sprachkultur-Pakete; abteilungsübergreifende Freigabepakete wirken erst nach Aktivierung durch die Administration als Rückfallebene.

### 7.2 Wer darf was pflegen

| Aktion | Erforderliche Rolle |
|------|----------|
| „Wissensdatenbank hochladen“ in der Kopfleiste (Datei erkennen → Paket wählen → importieren) | Abteilungsadministrator und höher |
| **Abteilungspakete** und **abteilungsübergreifende Pakete** anlegen/pflegen | Abteilungsadministrator und höher |
| **Organisationspaket (eigenes Unternehmen)** anlegen/pflegen, zentraler Schalter für abteilungsübergreifende Freigabe, Paket-Berechtigungen | Organisationsadministrator |
| Pflege der „Markennamen“ (festgelegte Marken-/Produktübersetzungen) | Rollen mit Zugriff auf das Wissensdatenbank-Panel |
| Zweisprachige Korpora / TMX importieren, TMX exportieren | Abteilungsadministrator und höher |
| Pflege der „Sprach- und Kulturregeln (Sicherheitsphrasen)“ | Abteilungsadministrator und höher |

### 7.3 Pakettypen

- **Organisationspaket (eigenes Unternehmen)**: gilt unternehmensweit;
- **Abteilungspaket (eigene Abteilung)**: nur innerhalb der eigenen Abteilung und der Organisationskette sichtbar — realisiert die Terminologie-Trennung auf Abteilungsebene;
- **Abteilungsübergreifendes Paket (geteilt)**: standardmäßig nicht verliehen; ob es auf andere Abteilungen wirkt, entscheiden gemeinsam der paketseitige Schalter „Abteilungsübergreifende Freigabe: Ein/Aus“ und der Unternehmensrichtwert.

### 7.4 Importwege

Öffnen Sie das Panel „Wissensdatenbank“ der Verwaltungskonsole (Abteilungsadministrator und höher):

- **Dateiimport**: Glossar/Dokument hochladen; das System erkennt es und schreibt die Einträge in das gewählte Paket;
- **Import bilingualer Texte / TMX**: Massenschreibvorgänge in den Übersetzungsspeicher, kompatibel mit TMX-Exporten gängiger CAT-Werkzeuge (Trados/memoQ usw.);
- **TMX-Export**: Die Unternehmens-Memories zurück in Ihre bisherige Werkzeugkette holen.

Plattformseitig werden zudem nach Branche automatisch allgemeine „Branchen- / Sprachkultur-Pakete“ zusammengestellt — ohne Pflegearbeit Ihres Unternehmens, beim Übersetzen automatisch wirksam.

### 7.5 Meine Berufsrolle (alle Nutzer)

Kontomenü → „Meine Berufsrolle“: wählen Sie Ihre Position aus dem vordefinierten Rollenwörterbuch (15+ Berufe). Beim Übersetzen stellt das System automatisch das passende Rollenpaket zusammen, damit Terminologie und Tonfall näher an Ihrem Arbeitskontext liegen.

---

## 8. Konto und persönliche Einstellungen

Einstiege: das Kontomenü oben rechts in der Kopfleiste sowie die Selfservice-Seiten im „Mehr“-Drawer.

| Eintrag | Pfad | Beschreibung |
|------|------|------|
| Mein Konto | Mehr → Mein Konto | Benutzername/E-Mail/Rolle/zugehöriger Mandant einsehen; Organisationsadministratoren und höher sehen hier die Konfigurationskarte **SCIM-Organisationssynchronisation** |
| Passwort ändern | Kontomenü → Passwort ändern | Kann bei der ersten Anmeldung verpflichtend sein |
| E-Mail-Adresse ändern | Kontomenü → E-Mail-Adresse ändern | Für Passwort-Wiederherstellung und Benachrichtigungen |
| Meine Berufsrolle | Kontomenü | Siehe 7.5 |
| Benachrichtigungszentrum | Glocke in der Kopfleiste | In-App-Nachrichten: Auftragsstatus, Freigabeergebnisse, Hinweise auf zurückgestufte Auslieferung usw.; einzelne oder alle als gelesen markieren |
| Konto deaktivieren | Kontomenü (gewöhnliche Nutzer) | Selfservice-Deaktivierung, wird nach Bestätigung gemäß Hinweis wirksam |
| Oberflächensprache | Sprachmenü der Kopfleiste | 12 Sprachen, sofort wirksam |

---

## 9. Organisations- und Mitgliederverwaltung (Organisationsadministrator)

Einstieg: Kontomenü → „Verwaltungskonsole“ → links „Organisation & Mitglieder“.

### 9.1 Organisationsstruktur

- **Neue Organisation / Neue Abteilung**: Pflege des mehrstufigen Organisationsbaums des Unternehmens; Umbenennen wird unterstützt.
- **Abteilungsbudget**: Für Abteilungen ein „Monatsbudget zuteilen“, das den Übersetzungsverbrauch der Abteilungsmitglieder steuert; den Mitgliedern wird der Budgetfortschritt in ihrer Oberfläche angezeigt.

### 9.2 Mitgliederkonten

Auf der Seite „Mitgliederkonten“ verwalten Sie alle Konten des Unternehmens:

- **Nutzer anlegen**: Einzelnes Mitglied erstellen, mit Rolle (gewöhnlicher Nutzer / Abteilungsadministrator / Organisationsadministrator) und Organisationszuordnung;
- **Nutzer per Stapelimport aus Excel**: Mehrere Mitglieder in einem Durchgang anlegen — die Startpasswörter gehen per E-Mail zu, und beim ersten Login müssen die Mitglieder ihr Passwort ändern;
- Für bestehende Mitglieder: **Passwort zurücksetzen**, Konto **aktivieren/deaktivieren**, Rolle und Organisationszugehörigkeit anpassen.

### 9.3 Mitarbeiter einladen

Auf der Seite „Mitarbeiter einladen“ erzeugen Sie **Einladungscodes** (mehrere möglich, jeweils mit Ziel-Ebene im Organisationsbaum). Geben Sie Organisationscode + Einladungscode an Ihre Kolleginnen und Kollegen weiter; damit melden sie sich auf der Registrierungsseite in der zuständigen Abteilung an.

### 9.4 SCIM-Organisationssynchronisation (optional)

Die SCIM-Karte unter „Mehr → Mein Konto“: Nutzt Ihr Unternehmen Identitätsquellen wie Okta oder Entra ID, lassen sich SCIM-Endpunkt und Token abrufen, um die Mitgliederanlage/-synchronisation zu automatisieren; nach der Aktivierung können Mitarbeitende über die SSO-Schaltfläche auf der Login-Seite ohne Passwort anmelden.

---

## 10. Externe Aufrufe und Integrationen (Organisationsadministrator)

Einstieg: Verwaltungskonsole → „Externe Aufrufe“. Drei Unterseiten: **OpenAPI**, **Callback-Benachrichtigungen**, **Offizielle SDKs**.

### 10.1 OpenAPI-Keys

- **API-Key ausstellen**: Der Klartext wird beim Anlegen **nur einmal** vollständig angezeigt — bewahren Sie ihn sofort sicher auf;
- Keys unterstützen **Aktivieren/Deaktivieren, Rotieren, tägliche Limits setzen, Löschen**;
- Der Key ist an Ihren Unternehmensmandanten und dessen Punktezähler gebunden — der über die API erzeugte Verbrauch läuft über dasselbe Konto wie im Web.

Die offene API bereit: asynchrone Übersetzungsaufträge (erstellen → abfragen → herunterladen, mit mehreren Dateien und Sprachen, Ergebnisse als ZIP paketierbar), synchrone Kurztextübersetzung (≤5000 Zeichen, ≤5 Sprachen), Guthabens- und Nutzungsabfragen, Wissensdatenbank-Statistiken. Die API-Dokumentation rufen Sie im Browser unter `/openapi/docs` Ihrer Site auf.

### 10.2 Webhook-Callbacks

Registrieren Sie eine Callback-URL und abonnieren Sie Ereignisse (z. B. `translation.completed`); bei abgeschlossenen Übersetzungen benachrichtigt die Plattform aktiv Ihr System. Unterstützt werden Signaturprüfung (X-Signature), Testversand, Einsicht in die Zustellhistorie und Wiederholungen.

### 10.3 Offizielle SDKs und Erweiterungen

Die Seite „Offizielle SDKs“ liefert Installationsanleitungen und Download-Einstiege für alle Enden:

| Ende | Bezugsweg | Zweck |
|----|----------|------|
| Python | `.whl` (oder Quellcode-`.tar.gz`) auf der Seite „Offizielles SDK" dieser Website herunterladen und lokal installieren: `pip install <heruntergeladene Datei>` (gehostet unter `/sdk/` auf dieser Website, nicht in externen Registries veröffentlicht) | Programmatische Übersetzung, Stapelaufgaben  |
| TypeScript | `.tgz` auf der Seite „Offizielles SDK" dieser Website herunterladen und lokal installieren: `npm install <heruntergeladene Datei>` (wie oben, gehostet unter `/sdk/` auf dieser Website) | Node/Frontend-Integration  |
| Java | Maven-Quellcode-Distribution | Anbindung interner Unternehmenssysteme |
| Browser-Erweiterung für Textauswahl | ZIP-Download auf dieser Seite (kein App-Store-Kanal) | Auf beliebigen Webseiten Text markieren → aufpoppende Schaltfläche „译“ anklicken → Übersetzung als Blase ansehen und kopieren; Tastenkürzel Alt+Shift+T (macOS: Alt+T) |
| Word-Add-in | Die Site stellt das Add-in-Manifest bereit; in Word „Einfügen → Add-ins abrufen → Mein Add-in hochladen“ seitlich laden | Markierten Text in Word übersetzen und ins Dokument zurückschreiben |
| VS-Code-Plugin | vscode-extension im Repository | Markierten Text im Editor an Ort und Stelle übersetzen, Begriffe nachschlagen |

**Erweiterungs-/Add-in-Konfiguration** (Popup der Auswahl-Erweiterung und Word-Aufgabenbereich mit identischen Feldern): **Dienstadresse** (Stammadresse der Site), **API-Key** (aus 10.1), **Zielsprache** und **Übersetzungsmodus** (Schnell/Profi) eintragen — Terminologie und Übersetzungsspeicher entsprechen dem Stand im Web.

⚠ Falls nach einem Erweiterungs-Update das Aussehen unverändert wirkt: Überschreiben Sie mit dem neuen ZIP **dasselbe Verzeichnis** und klicken Sie auf `chrome://extensions` auf der Karte der Erweiterung die Schaltfläche **Aktualisieren**.

### 10.4 Grenzen für Abteilungsadministratoren

Benötigt ein Abteilungsadministrator eine API-Integration, wenden Sie sich an den Organisationsadministrator zur Key-Ausstellung; das Menü „Externe Aufrufe“ wird Abteilungsadministratoren nicht angezeigt.

---

## 11. Benachrichtigungen, Feedback und KI-Assistent

### 11.1 Benachrichtigungszentrum (alle Nutzer)

Die Glocke in der Kopfleiste: ungelesene Meldungen werden im 30-Sekunden-Takt abgefragt; das Dropdown zeigt In-App-Nachrichten (Auftrag abgeschlossen, Freigabeergebnis, Hinweis zurückgestufte Auslieferung usw.), einzelne oder alle als gelesen markierbar.

### 11.2 Feedback einreichen und bearbeiten

- **Alle Nutzer**: Auf dem Detail abgeschlossener Aufträge „Feedback übermitteln“ und die problematischen Abschnitte samt Erwartung beschreiben.
- **Abteilungs-/Organisationsadministratoren**: In der Verwaltungskonsole unter „Feedback/Freigaben“ die Feedbacks des Mandanten einsehen und **antworten**; auf derselben Seite verarbeitet der „Freigabe-Arbeitsplatz“ Aufträge mit Status „Freigabe ausstehend“ (Freigeben/Ablehnen).
- Panel „Übersicht“ (Abteilungsadministrator und höher): Betriebs-Dashboard des Unternehmens und Nutzungsdetails (Nutzung als CSV exportierbar).

### 11.3 KI-Assistent-Widget (alle Nutzer sowie Besucher)

Die Schaltfläche „KI-Assistent“ unten rechts ruft jederzeit einen Dialog auf: Produktfragen, Funktionsführungen, schnelle Direktzugriffe. Beim Neuladen der Seite geht der Dialog nicht verloren (lokaler Zwischenspeicher → automatische Wiederherstellung aus der Cloud-Historie).

---

## 12. Häufige Fragen

**Q1: Der Übersetzen-Button reagiert nicht / Hinweis auf unzureichendes Kontingent?**
Das aktuelle Guthaben oder das Abteilungsbudget ist aufgebraucht. Gewöhnliche Nutzer wenden sich bitte an die Unternehmensadministration; Administratoren sehen die Nutzungsdetails in der Verwaltungskonsole.

**Q2: Wo ist die Dateiübersetzung? Warum nimmt das Chat-Feld keine Anhänge mehr an?**
Dateiübersetzungen laufen ausschließlich über „Übersetzungsaufträge → Datei“; der Chat dient nur der reinen Textübersetzung.

**Q3: PDF-Übersetzung wurde abgelehnt?**
PDFs über 40 MB oder 120 Seiten bitte zuerst in docx konvertieren und dann den Auftrag anlegen; gescannte PDFs (seitenweise nur Bilder) werden derzeit nicht unterstützt.

**Q4: Warum weicht die Übersetzung von meinem Glossar ab?**
Prüfen Sie, ob die Begriffe in die Wissensdatenbank importiert wurden und das Paket für Ihre Abteilung sichtbar ist (Kapitel 7); für offizielle Inhalte wählen Sie bitte den Modus „Profi“.

**Q5: Das ausgelieferte docx weicht im Layout vom Original ab?**
Der Modus „Format wiederherstellen“ bewahrt das Layout in der überwiegenden Mehrheit der Fälle; vereinzelt komplexe Layouts werden auf .md-Textauslieferung zurückgestuft und per In-App-Nachricht gemeldet. Laden Sie die .md herunter und prüfen Sie sie im Paralleleditor, oder übermitteln Sie Feedback.

**Q6: Direkt nach der Übersetzung hat sich der Guthabenwert nicht geändert?**
Die Messung wird im Abschlusszeitpunkt synchron in der Datenbank verbucht; laden Sie die Seite einmal neu. Bleibt die Abweichung, übermitteln Sie bitte Feedback.

**Q7: Funktioniert es auch auf Smartphone/Tablet?**
Rufen Sie die Site einfach im Browser auf; die Oberfläche passt sich an schmale Bildschirme an. Erweiterungen und Add-ins sind primär für Desktop ausgelegt.

**Q8: Anzeigetexte werden abgeschnitten oder wirken seltsam (z. B. Arabisch)?**
Wechseln Sie zuerst in eine andere Sprache, um zu prüfen, ob es ein einzelner Eintragstext ist, und senden Sie dann über den Feedback-Kanal einen Screenshot.

---

## 13. Anhang

### A. Schnellreferenz der UI-Namen (简体中文 / Deutsch)

| 简体中文 | Deutsch |
|----------|---------|
| 即时翻译 | Sofortübersetzung |
| 翻译工单 | Übersetzungsaufträge |
| 对照编辑 | Paralleleditor |
| 更多 | Mehr |
| 我的账号 | Mein Konto |
| 上传知识库 | Wissensdatenbank hochladen |
| 进入管理后台 | Verwaltungskonsole |
| 通知中心 | Benachrichtigungen |
| 专业校对 / 快速 | Profi / Schnell |
| 缩翻 | Kurzfassung |
| 自动检测 | Automatisch erkennen |
| 知识库语言 / 其他语言（AI翻译） | Wissensdatenbanksprachen / Andere Sprachen (AI) |
| 创建翻译工单 | Übersetzungsauftrag erstellen |
| 交付方式：还原文件模式 / 纯文案模式 | Ausgabemodus: Format wiederherstellen / Nur Text |
| 我的工单 | Meine Aufträge |
| 排队中 / 翻译中 / 待审批 / 已批准 / 已驳回 / 已完成 / 已取消 | In Warteschlange / Übersetzung läuft / Freigabe ausstehend / Genehmigt / Abgelehnt / Abgeschlossen / Abgebrochen |
| 执行进度 | Fortschritt |
| 质检报告 | Qualitätsbericht |
| 审批台 / 批准 / 驳回 | Freigabe-Arbeitsplatz / Freigeben / Ablehnen |
| 知识库 | Wissensdatenbank |
| 组织包（本企业）/ 部门包（本部门）/ 跨部门包（共享） | Mandantenpaket (eigenes Unternehmen) / Abteilungspaket / Abteilungsübergreifendes Paket (geteilt) |
| 文件导入 / 双语语料 · TMX 导入 | Dateiimport / Import bilingualer Texte / TMX |
| 语言文化规范（安全句） | Sprach- und Kulturregeln (Sicherheitsphrasen · Qualitätsschranke) |
| 组织与成员 | Organisation & Mitglieder |
| 组织结构 / 邀请员工 / 成员账户 | Organisation / Mitarbeiter einladen / Mitgliederkonten |
| 部门预算 | Abteilungsbudget |
| 开通用户 / Excel 批量导入用户 | Nutzer anlegen / Nutzer per Stapelimport aus Excel |
| 邀请码管理 / 生成邀请码 | Einladungscodes / Code erzeugen |
| 普通用户 / 部门管理员 / 组织管理员 | Nutzer / Abteilungsadministrator / Organisationsadministrator |
| 外部调用 / 开放 API / 回调通知 / 官方 SDK | Externe Aufrufe / OpenAPI / Callback-Benachrichtigungen / Offizielle SDKs |
| 我的职业角色 | Meine Berufsrolle |
| 修改密码 / 修改邮箱 / 注销账号 | Passwort ändern / E-Mail-Adresse ändern / Konto deaktivieren |
| 积分 | Punkte |

### B. Unterstützte Oberflächensprachen (12)

简体中文 · 繁體中文 · English · 日本語 · 한국어 · Deutsch · Français · Español · Português · Русский · العربية (RTL-Layout) · ไทย

### C. Tastenkürzel im Überblick

| Tastenkürzel | Funktion |
|--------|------|
| Strg/⌘ + K | Sofortübersetzung: in dieser Unterhaltung suchen |
| Alt+Shift+T (macOS: Alt+T) | Browser-Erweiterung: markierten Text übersetzen |
| Cmd/Ctrl+Alt+T | VS-Code-Plugin: markierten Text an Ort und Stelle übersetzen |

### D. Dateiübersetzungsformate

Siehe Tabelle in [5.1 Auftrag erstellen](#51-auftrag-erstellen).

---

*Dieses Handbuch beschreibt die im Mandanten verfügbaren Produktfunktionen. Plattformbetrieb, kaufmännische und abrechnungsrelevante Themen richten Sie bitte an die Administration Ihres Unternehmens oder das Plattform-Service-Team.*
