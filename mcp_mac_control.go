package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	htmlpkg "html"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const macControlTimeout = 30 * time.Second

var notesJXAHelpers = `
const Notes = Application('Notes');
function safeFolders(owner) { try { return owner.folders(); } catch (e) { return []; } }
function safeNotes(folder) { try { return folder.notes(); } catch (e) { return []; } }
function safeName(object) { try { return String(object.name()); } catch (e) { return ''; } }
function safeID(object) { try { return String(object.id()); } catch (e) { return ''; } }
function escapeHTML(value) {
  return String(value).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;');
}
function isoDate(value) { try { return value ? value.toISOString() : null; } catch (e) { return null; } }
function matchingAccounts(want) {
  const accounts = Notes.accounts();
  if (!want) return accounts;
  return accounts.filter(a => safeName(a).toLowerCase() === String(want).toLowerCase());
}
function rootFolders(account) {
  const every = safeFolders(account);
  const childIDs = {};
  every.forEach(parent => safeFolders(parent).forEach(child => {
    const id = safeID(child);
    if (id) childIDs[id] = true;
  }));
  return every.filter(folder => {
    const id = safeID(folder);
    return id && !childIDs[id];
  });
}
function resolveFolder(path, accountName) {
  const parts = String(path || '').split('/').map(s => s.trim()).filter(Boolean);
  if (!parts.length) throw new Error('folderPath is required');
  const found = [];
  matchingAccounts(accountName).forEach(account => {
    let folders = rootFolders(account);
    let folder = null;
    const actual = [];
    for (const part of parts) {
      const matches = folders.filter(f => safeName(f).toLowerCase() === part.toLowerCase());
      if (matches.length !== 1) { folder = null; break; }
      folder = matches[0];
      actual.push(safeName(folder));
      folders = safeFolders(folder);
    }
    if (folder) found.push({ folder, account: safeName(account), path: actual.join('/') });
  });
  if (!found.length) throw new Error('folder not found: ' + path);
  if (found.length > 1) throw new Error('folder path exists in multiple accounts; pass account');
  return found[0];
}
function walkFolders(folders, accountName, parentPath, out, seen) {
  folders.forEach(folder => {
    const id = safeID(folder);
    const name = safeName(folder);
    if (!id || !name || seen[id]) return;
    seen[id] = true;
    const path = parentPath ? parentPath + '/' + name : name;
    out.push({ folder, account: accountName, path });
    walkFolders(safeFolders(folder), accountName, path, out, seen);
  });
}
function allFolders(accountName) {
  const out = [];
  matchingAccounts(accountName).forEach(account => walkFolders(rootFolders(account), safeName(account), '', out, {}));
  return out;
}
function noteInfo(note, location, includeBody, includeHTML) {
  const info = {
    id: safeID(note),
    title: safeName(note),
    account: location.account,
    folderPath: location.path,
    created: isoDate(note.creationDate ? note.creationDate() : null),
    modified: isoDate(note.modificationDate ? note.modificationDate() : null)
  };
  if (includeBody) info.body = String(note.plaintext() || '');
  if (includeHTML) info.html = String(note.body() || '');
  return info;
}
function notesInLocations(locations, recursive) {
  const expanded = [];
  const seen = {};
  function add(location) {
    if (seen[location.account + ':' + location.path]) return;
    seen[location.account + ':' + location.path] = true;
    expanded.push(location);
    if (recursive) safeFolders(location.folder).forEach(child => {
      const name = safeName(child);
      if (name) add({ folder: child, account: location.account, path: location.path + '/' + name });
    });
  }
  locations.forEach(add);
  return expanded;
}
function findNotes(id, title, folderPath, accountName) {
  let locations;
  if (folderPath) locations = [resolveFolder(folderPath, accountName)];
  else locations = allFolders(accountName);
  const matches = [];
  locations.forEach(location => safeNotes(location.folder).forEach(note => {
    const noteId = safeID(note);
    const noteTitle = safeName(note);
    if (id && noteId === id) matches.push({ note, location });
    else if (!id && title && noteTitle === title) matches.push({ note, location });
  }));
  return matches;
}
`

func newMacControlMCPServer() *mcpServer {
	readOnly := map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false}
	additive := map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": false}
	mutating := map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": false, "openWorldHint": false}
	server := &mcpServer{
		Name: "acc-mac-control", Version: "3.0.1",
		Tools: []mcpTool{
			{Name: "calendar_list_events", Description: "List local Calendar events. ACC closes Calendar afterward only if this tool opened it.", Annotations: readOnly, InputSchema: objectSchema(map[string]any{"start": stringProperty("ISO 8601 start, default now"), "end": stringProperty("ISO 8601 end, default 7 days later"), "calendar": stringProperty("Exact calendar name")}, nil)},
			{Name: "calendar_create_event", Description: "Create a local Calendar event.", Annotations: additive, InputSchema: objectSchema(map[string]any{"title": stringProperty("Event title"), "start": stringProperty("ISO 8601 start"), "end": stringProperty("ISO 8601 end, default one hour later"), "calendar": stringProperty("Exact calendar name"), "location": stringProperty("Location"), "notes": stringProperty("Event notes"), "alarmMinutesBefore": map[string]any{"type": "integer", "minimum": 0}}, []string{"title", "start"})},
			{Name: "calendar_delete_event", Description: "Delete one exact Calendar event. Refuses ambiguous matches unless start is supplied.", Annotations: mutating, InputSchema: objectSchema(map[string]any{"title": stringProperty("Exact event title"), "start": stringProperty("ISO 8601 start to disambiguate"), "calendar": stringProperty("Exact calendar name")}, []string{"title"})},
			{Name: "reminders_list", Description: "List local Reminders items.", Annotations: readOnly, InputSchema: objectSchema(map[string]any{"list": stringProperty("Exact list name"), "includeCompleted": map[string]any{"type": "boolean"}}, nil)},
			{Name: "reminders_create", Description: "Create a local Reminder.", Annotations: additive, InputSchema: objectSchema(map[string]any{"title": stringProperty("Reminder title"), "list": stringProperty("Exact list name"), "dueDate": stringProperty("ISO 8601 due date"), "priority": map[string]any{"type": "integer", "minimum": 0, "maximum": 9}, "notes": stringProperty("Reminder notes")}, []string{"title"})},
			{Name: "reminders_complete", Description: "Complete one exact Reminder, optionally scoped to a list.", Annotations: mutating, InputSchema: objectSchema(map[string]any{"name": stringProperty("Exact reminder name"), "list": stringProperty("Exact list name")}, []string{"name"})},
			{Name: "notes_folders", Description: "Return the complete Apple Notes folder tree as stable account + folderPath values. Also reports note counts and whether each folder has an Instructions note.", Annotations: readOnly, InputSchema: objectSchema(map[string]any{"account": stringProperty("Optional exact account name"), "instructionTitle": stringProperty("Folder guide title, default Instructions")}, nil)},
			{Name: "notes_folder_guide", Description: "Read the Instructions note inside one exact folder path. Use notes_folders first when the path is unknown.", Annotations: readOnly, InputSchema: objectSchema(map[string]any{"folderPath": stringProperty("Folder path such as Stillness/Sleep"), "account": stringProperty("Optional exact account name"), "title": stringProperty("Guide title, default Instructions")}, []string{"folderPath"})},
			{Name: "notes_list", Description: "List notes by folderPath without requiring IDs. Returns metadata newest first; bodies are opt-in.", Annotations: readOnly, InputSchema: objectSchema(map[string]any{"folderPath": stringProperty("Exact nested folder path"), "account": stringProperty("Optional exact account name"), "recursive": map[string]any{"type": "boolean"}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}, "includeBody": map[string]any{"type": "boolean"}}, []string{"folderPath"})},
			{Name: "notes_recent", Description: "Return IDs and metadata for the newest 1, 3, or 7 notes in one exact folderPath.", Annotations: readOnly, InputSchema: objectSchema(map[string]any{"folderPath": stringProperty("Exact nested folder path"), "account": stringProperty("Optional exact account name"), "count": map[string]any{"type": "integer", "enum": []int{1, 3, 7}}}, []string{"folderPath", "count"})},
			{Name: "notes_get", Description: "Read one Apple Note by ID, or by exact title plus folderPath. IDs are optional when a folder path and title identify the note.", Annotations: readOnly, InputSchema: objectSchema(map[string]any{"id": stringProperty("Note ID"), "title": stringProperty("Exact note title"), "folderPath": stringProperty("Exact nested folder path"), "account": stringProperty("Optional exact account name"), "includeHTML": map[string]any{"type": "boolean"}}, nil)},
			{Name: "notes_search", Description: "Search Apple Note titles and plaintext, optionally scoped to one nested folder path.", Annotations: readOnly, InputSchema: objectSchema(map[string]any{"query": stringProperty("Search text"), "folderPath": stringProperty("Optional exact nested folder path"), "account": stringProperty("Optional exact account name"), "recursive": map[string]any{"type": "boolean"}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}}, []string{"query"})},
			{Name: "notes_create", Description: "Create a note in an existing folderPath. The folder must already exist.", Annotations: additive, InputSchema: objectSchema(map[string]any{"folderPath": stringProperty("Exact nested folder path"), "account": stringProperty("Optional exact account name"), "title": stringProperty("Note title"), "body": stringProperty("Plaintext body")}, []string{"folderPath", "title"})},
			{Name: "notes_append", Description: "Append plaintext to a note by ID or exact title + folderPath while preserving its existing HTML formatting.", Annotations: additive, InputSchema: objectSchema(map[string]any{"id": stringProperty("Note ID"), "title": stringProperty("Exact note title"), "folderPath": stringProperty("Exact nested folder path"), "account": stringProperty("Optional exact account name"), "text": stringProperty("Plaintext to append")}, []string{"text"})},
			{Name: "notes_replace", Description: "Replace a note body while preserving its existing title. Accepts ID or exact title + folderPath and requires confirm=true.", Annotations: mutating, InputSchema: objectSchema(map[string]any{"id": stringProperty("Note ID"), "title": stringProperty("Exact note title"), "folderPath": stringProperty("Exact nested folder path"), "account": stringProperty("Optional exact account name"), "text": stringProperty("New plaintext body"), "confirm": map[string]any{"type": "boolean", "description": "Must be true"}}, []string{"text", "confirm"})},
			{Name: "notes_delete", Description: "Delete one Apple Note by ID or exact title + folderPath. Requires confirm=true and refuses ambiguous matches.", Annotations: mutating, InputSchema: objectSchema(map[string]any{"id": stringProperty("Note ID"), "title": stringProperty("Exact note title"), "folderPath": stringProperty("Exact nested folder path"), "account": stringProperty("Optional exact account name"), "confirm": map[string]any{"type": "boolean", "description": "Must be true"}}, []string{"confirm"})},
			{Name: "notify", Description: "Show a local macOS banner notification.", Annotations: additive, InputSchema: objectSchema(map[string]any{"message": stringProperty("Notification body"), "title": stringProperty("Title"), "subtitle": stringProperty("Subtitle"), "sound": stringProperty("System sound name")}, []string{"message"})},
		},
		Handlers: map[string]mcpToolHandler{},
	}

	server.Handlers["calendar_list_events"] = calendarListEvents
	server.Handlers["calendar_create_event"] = calendarCreateEvent
	server.Handlers["calendar_delete_event"] = calendarDeleteEvent
	server.Handlers["reminders_list"] = remindersList
	server.Handlers["reminders_create"] = remindersCreate
	server.Handlers["reminders_complete"] = remindersComplete
	server.Handlers["notes_folders"] = notesFolders
	server.Handlers["notes_folder_guide"] = notesFolderGuide
	server.Handlers["notes_list"] = notesList
	server.Handlers["notes_recent"] = notesRecent
	server.Handlers["notes_get"] = notesGet
	server.Handlers["notes_search"] = notesSearch
	server.Handlers["notes_create"] = notesCreate
	server.Handlers["notes_append"] = notesAppend
	server.Handlers["notes_replace"] = notesReplace
	server.Handlers["notes_delete"] = notesDelete
	server.Handlers["notify"] = macNotify
	return server
}

func stringProperty(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func normalizeNotesFolderPath(value string) (string, error) {
	parts := strings.Split(strings.TrimSpace(value), "/")
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if part == "." || part == ".." {
			return "", fmt.Errorf("folderPath cannot contain . or ..")
		}
		cleaned = append(cleaned, part)
	}
	if len(cleaned) == 0 {
		return "", fmt.Errorf("folderPath is required")
	}
	return strings.Join(cleaned, "/"), nil
}

func validateRecentCount(count int) error {
	if count != 1 && count != 3 && count != 7 {
		return fmt.Errorf("count must be 1, 3, or 7")
	}
	return nil
}

func normalizedFolderArg(args map[string]any) (string, error) {
	path, err := requiredString(args, "folderPath")
	if err != nil {
		return "", err
	}
	return normalizeNotesFolderPath(path)
}

func notesFolders(ctx context.Context, args map[string]any) (any, error) {
	instructionTitle := optionalString(args, "instructionTitle")
	if instructionTitle == "" {
		instructionTitle = "Instructions"
	}
	body := notesJXAHelpers + fmt.Sprintf(`
const guideTitle = %s.toLowerCase();
const out = allFolders(%s).map(location => {
  const notes = safeNotes(location.folder);
  const guide = notes.find(note => safeName(note).toLowerCase() === guideTitle);
  return {
    account: location.account,
    folderPath: location.path,
    noteCount: notes.length,
    childFolderCount: safeFolders(location.folder).length,
    instructionNoteId: guide ? safeID(guide) : null
  };
});
out.sort((a, b) => (a.account + '/' + a.folderPath).localeCompare(b.account + '/' + b.folderPath));
return JSON.stringify(out);`, jxaLiteral(instructionTitle), jxaLiteral(optionalString(args, "account")))
	return runManagedJXA(ctx, body, "Notes")
}

func notesFolderGuide(ctx context.Context, args map[string]any) (any, error) {
	path, err := normalizedFolderArg(args)
	if err != nil {
		return nil, err
	}
	title := optionalString(args, "title")
	if title == "" {
		title = "Instructions"
	}
	body := notesJXAHelpers + fmt.Sprintf(`
const location = resolveFolder(%s, %s);
const matches = safeNotes(location.folder).filter(note => safeName(note).toLowerCase() === %s.toLowerCase());
if (!matches.length) return JSON.stringify({ok:false,error:'guide note not found',title:%s,account:location.account,folderPath:location.path});
if (matches.length > 1) return JSON.stringify({ok:false,error:'multiple guide notes found',matches:matches.map(n => noteInfo(n, location, false, false))});
return JSON.stringify(Object.assign({ok:true}, noteInfo(matches[0], location, true, false)));`, jxaLiteral(path), jxaLiteral(optionalString(args, "account")), jxaLiteral(title), jxaLiteral(title))
	return runManagedJXA(ctx, body, "Notes")
}

func notesList(ctx context.Context, args map[string]any) (any, error) {
	path, err := normalizedFolderArg(args)
	if err != nil {
		return nil, err
	}
	limit := intArg(args, "limit", 50)
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	body := notesJXAHelpers + fmt.Sprintf(`
const root = resolveFolder(%s, %s);
const locations = notesInLocations([root], %s);
let out = [];
locations.forEach(location => safeNotes(location.folder).forEach(note => out.push(noteInfo(note, location, %s, false))));
out.sort((a, b) => String(b.modified || '').localeCompare(String(a.modified || '')));
return JSON.stringify(out.slice(0, %d));`, jxaLiteral(path), jxaLiteral(optionalString(args, "account")), jxaLiteral(boolArg(args, "recursive", false)), jxaLiteral(boolArg(args, "includeBody", false)), limit)
	return runManagedJXA(ctx, body, "Notes")
}

func notesRecent(ctx context.Context, args map[string]any) (any, error) {
	path, err := normalizedFolderArg(args)
	if err != nil {
		return nil, err
	}
	count := intArg(args, "count", 0)
	if err := validateRecentCount(count); err != nil {
		return nil, err
	}
	body := notesJXAHelpers + fmt.Sprintf(`
const location = resolveFolder(%s, %s);
const out = safeNotes(location.folder).map(note => noteInfo(note, location, false, false));
out.sort((a, b) => String(b.modified || '').localeCompare(String(a.modified || '')));
return JSON.stringify(out.slice(0, %d));`, jxaLiteral(path), jxaLiteral(optionalString(args, "account")), count)
	return runManagedJXA(ctx, body, "Notes")
}

func notesGet(ctx context.Context, args map[string]any) (any, error) {
	id := optionalString(args, "id")
	title := optionalString(args, "title")
	if id == "" && title == "" {
		return nil, fmt.Errorf("id or title is required")
	}
	path := optionalString(args, "folderPath")
	if path != "" {
		var err error
		path, err = normalizeNotesFolderPath(path)
		if err != nil {
			return nil, err
		}
	}
	body := notesJXAHelpers + fmt.Sprintf(`
const matches = findNotes(%s, %s, %s, %s);
if (!matches.length) return JSON.stringify({ok:false,error:'note not found'});
if (matches.length > 1) return JSON.stringify({ok:false,error:'multiple matches; pass id or folderPath',matches:matches.map(m => noteInfo(m.note,m.location,false,false))});
return JSON.stringify(Object.assign({ok:true}, noteInfo(matches[0].note, matches[0].location, true, %s)));`, jxaLiteral(id), jxaLiteral(title), jxaLiteral(path), jxaLiteral(optionalString(args, "account")), jxaLiteral(boolArg(args, "includeHTML", false)))
	return runManagedJXA(ctx, body, "Notes")
}

func notesSearch(ctx context.Context, args map[string]any) (any, error) {
	query, err := requiredString(args, "query")
	if err != nil {
		return nil, err
	}
	path := optionalString(args, "folderPath")
	if path != "" {
		path, err = normalizeNotesFolderPath(path)
		if err != nil {
			return nil, err
		}
	}
	limit := intArg(args, "limit", 50)
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	body := notesJXAHelpers + fmt.Sprintf(`
let locations = %s ? [resolveFolder(%s, %s)] : allFolders(%s);
locations = notesInLocations(locations, %s);
const q = %s.toLowerCase();
const out = [];
locations.forEach(location => safeNotes(location.folder).forEach(note => {
  const title = String(note.name() || '');
  const text = String(note.plaintext() || '');
  const index = text.toLowerCase().indexOf(q);
  if (title.toLowerCase().indexOf(q) >= 0 || index >= 0) {
    const info = noteInfo(note, location, false, false);
    const start = Math.max(0, index - 60);
    info.snippet = index >= 0 ? text.slice(start, index + q.length + 120).replace(/\n/g, ' ').trim() : text.slice(0, 180).replace(/\n/g, ' ').trim();
    out.push(info);
  }
}));
out.sort((a, b) => String(b.modified || '').localeCompare(String(a.modified || '')));
return JSON.stringify(out.slice(0, %d));`, jxaLiteral(path != ""), jxaLiteral(path), jxaLiteral(optionalString(args, "account")), jxaLiteral(optionalString(args, "account")), jxaLiteral(boolArg(args, "recursive", false)), jxaLiteral(query), limit)
	return runManagedJXA(ctx, body, "Notes")
}

func notesCreate(ctx context.Context, args map[string]any) (any, error) {
	path, err := normalizedFolderArg(args)
	if err != nil {
		return nil, err
	}
	title, err := requiredString(args, "title")
	if err != nil {
		return nil, err
	}
	htmlBody := textToNotesHTML(optionalString(args, "body"))
	body := notesJXAHelpers + fmt.Sprintf(`
const location = resolveFolder(%s, %s);
const duplicate = safeNotes(location.folder).filter(note => String(note.name()) === %s);
if (duplicate.length) return JSON.stringify({ok:false,error:'note already exists',matches:duplicate.map(n => noteInfo(n,location,false,false))});
const note = Notes.Note({name:%s, body:%s});
location.folder.notes.push(note);
return JSON.stringify(Object.assign({ok:true}, noteInfo(note, location, false, false)));`, jxaLiteral(path), jxaLiteral(optionalString(args, "account")), jxaLiteral(title), jxaLiteral(title), jxaLiteral(htmlBody))
	return runManagedJXA(ctx, body, "Notes")
}

func notesAppend(ctx context.Context, args map[string]any) (any, error) {
	text, err := requiredString(args, "text")
	if err != nil {
		return nil, err
	}
	return mutateNote(ctx, args, textToNotesHTML(text), false)
}

func notesReplace(ctx context.Context, args map[string]any) (any, error) {
	text, err := requiredString(args, "text")
	if err != nil {
		return nil, err
	}
	if !boolArg(args, "confirm", false) {
		return nil, fmt.Errorf("confirm must be true for notes_replace")
	}
	return mutateNote(ctx, args, textToNotesHTML(text), true)
}

func notesDelete(ctx context.Context, args map[string]any) (any, error) {
	if !boolArg(args, "confirm", false) {
		return nil, fmt.Errorf("confirm must be true for notes_delete")
	}
	id := optionalString(args, "id")
	title := optionalString(args, "title")
	if id == "" && title == "" {
		return nil, fmt.Errorf("id or title is required")
	}
	path := optionalString(args, "folderPath")
	if path != "" {
		var err error
		path, err = normalizeNotesFolderPath(path)
		if err != nil {
			return nil, err
		}
	}
	body := notesJXAHelpers + fmt.Sprintf(`
const matches = findNotes(%s, %s, %s, %s);
if (!matches.length) return JSON.stringify({ok:false,error:'note not found'});
if (matches.length > 1) return JSON.stringify({ok:false,error:'multiple matches; pass id or folderPath',matches:matches.map(m => noteInfo(m.note,m.location,false,false))});
const target = matches[0];
const deleted = noteInfo(target.note, target.location, false, false);
target.note.delete();
return JSON.stringify({ok:true,deleted});`, jxaLiteral(id), jxaLiteral(title), jxaLiteral(path), jxaLiteral(optionalString(args, "account")))
	return runManagedJXA(ctx, body, "Notes")
}

func mutateNote(ctx context.Context, args map[string]any, htmlBody string, replace bool) (any, error) {
	id := optionalString(args, "id")
	title := optionalString(args, "title")
	if id == "" && title == "" {
		return nil, fmt.Errorf("id or title is required")
	}
	path := optionalString(args, "folderPath")
	if path != "" {
		var err error
		path, err = normalizeNotesFolderPath(path)
		if err != nil {
			return nil, err
		}
	}
	body := buildNoteMutationJXA(id, title, path, optionalString(args, "account"), htmlBody, replace)
	return runManagedJXA(ctx, body, "Notes")
}

func buildNoteMutationJXA(id, title, path, account, htmlBody string, replace bool) string {
	return notesJXAHelpers + fmt.Sprintf(`
const matches = findNotes(%s, %s, %s, %s);
if (!matches.length) return JSON.stringify({ok:false,error:'note not found'});
if (matches.length > 1) return JSON.stringify({ok:false,error:'multiple matches; pass id or folderPath',matches:matches.map(m => noteInfo(m.note,m.location,false,false))});
const target = matches[0];
const originalTitle = safeName(target.note);
const before = String(target.note.plaintext() || '');
const fragment = %s;
if (%s) target.note.body = '<div>' + escapeHTML(originalTitle) + '</div>' + fragment;
else target.note.body = String(target.note.body() || '') + fragment;
const result = noteInfo(target.note, target.location, false, false);
result.originalTitle = originalTitle;
result.titlePreserved = result.title === originalTitle;
result.ok = result.titlePreserved;
if (!result.titlePreserved) result.error = 'Apple Notes changed the title while updating the body';
result.previousCharacterCount = before.length;
result.newCharacterCount = String(target.note.plaintext() || '').length;
return JSON.stringify(result);`, jxaLiteral(id), jxaLiteral(title), jxaLiteral(path), jxaLiteral(account), jxaLiteral(htmlBody), jxaLiteral(replace))
}

func calendarListEvents(ctx context.Context, args map[string]any) (any, error) {
	start := optionalString(args, "start")
	if start == "" {
		start = time.Now().Format(time.RFC3339)
	}
	end := optionalString(args, "end")
	if end == "" {
		startTime, err := time.Parse(time.RFC3339, start)
		if err != nil {
			return nil, fmt.Errorf("start must be ISO 8601: %w", err)
		}
		end = startTime.Add(7 * 24 * time.Hour).Format(time.RFC3339)
	}
	body := fmt.Sprintf(`
const Cal = Application('Calendar');
const startDate = new Date(%s);
const endDate = new Date(%s);
let calendars = Cal.calendars();
const wanted = %s;
if (wanted) calendars = calendars.filter(calendar => String(calendar.name()) === wanted);
const out = [];
calendars.forEach(calendar => {
  let events = [];
  try { events = calendar.events.whose({_and:[{startDate:{_greaterThan:startDate}},{startDate:{_lessThan:endDate}}]})(); } catch (e) {}
  events.forEach(event => out.push({title:String(event.summary()),start:event.startDate().toISOString(),end:event.endDate().toISOString(),location:String(event.location()||''),calendar:String(calendar.name()),notes:event.description?String(event.description()||''):''}));
});
out.sort((a,b) => a.start.localeCompare(b.start));
return JSON.stringify(out);`, jxaLiteral(start), jxaLiteral(end), jxaLiteral(optionalString(args, "calendar")))
	return runManagedJXA(ctx, body, "Calendar")
}

func calendarCreateEvent(ctx context.Context, args map[string]any) (any, error) {
	title, err := requiredString(args, "title")
	if err != nil {
		return nil, err
	}
	start, err := requiredString(args, "start")
	if err != nil {
		return nil, err
	}
	startTime, err := time.Parse(time.RFC3339, start)
	if err != nil {
		return nil, fmt.Errorf("start must be ISO 8601: %w", err)
	}
	end := optionalString(args, "end")
	if end == "" {
		end = startTime.Add(time.Hour).Format(time.RFC3339)
	}
	body := fmt.Sprintf(`
const Cal = Application('Calendar');
let calendar = Cal.calendars()[0];
const wanted = %s;
if (wanted) { const matches = Cal.calendars().filter(c => String(c.name()) === wanted); if (!matches.length) return JSON.stringify({ok:false,error:'calendar not found'}); calendar = matches[0]; }
const props = {summary:%s,startDate:new Date(%s),endDate:new Date(%s)};
if (%s) props.location = %s;
if (%s) props.description = %s;
const event = Cal.Event(props);
calendar.events.push(event);
const alarmMinutes = %d;
if (alarmMinutes >= 0) event.displayAlarms.push(Cal.DisplayAlarm({trigger:-alarmMinutes*60}));
return JSON.stringify({ok:true,title:String(event.summary()),start:event.startDate().toISOString(),calendar:String(calendar.name())});`, jxaLiteral(optionalString(args, "calendar")), jxaLiteral(title), jxaLiteral(start), jxaLiteral(end), jxaLiteral(optionalString(args, "location") != ""), jxaLiteral(optionalString(args, "location")), jxaLiteral(optionalString(args, "notes") != ""), jxaLiteral(optionalString(args, "notes")), intArg(args, "alarmMinutesBefore", -1))
	return runManagedJXA(ctx, body, "Calendar")
}

func calendarDeleteEvent(ctx context.Context, args map[string]any) (any, error) {
	title, err := requiredString(args, "title")
	if err != nil {
		return nil, err
	}
	body := fmt.Sprintf(`
const Cal = Application('Calendar');
let calendars = Cal.calendars();
const wantedCalendar = %s;
if (wantedCalendar) calendars = calendars.filter(c => String(c.name()) === wantedCalendar);
const wantedTitle = %s;
const wantedStart = %s;
const matches = [];
calendars.forEach(calendar => { let events=[]; try { events=calendar.events.whose({summary:wantedTitle})(); } catch(e) {} events.forEach(event => { const start=event.startDate().toISOString(); if (!wantedStart || start===new Date(wantedStart).toISOString()) matches.push({event,info:{title:String(event.summary()),start,calendar:String(calendar.name())}}); }); });
if (!matches.length) return JSON.stringify({ok:false,error:'event not found'});
if (matches.length > 1) return JSON.stringify({ok:false,error:'multiple matches; pass start',matches:matches.map(m=>m.info)});
matches[0].event.delete();
return JSON.stringify({ok:true,deleted:matches[0].info});`, jxaLiteral(optionalString(args, "calendar")), jxaLiteral(title), jxaLiteral(optionalString(args, "start")))
	return runManagedJXA(ctx, body, "Calendar")
}

func remindersList(ctx context.Context, args map[string]any) (any, error) {
	body := fmt.Sprintf(`
const Rem = Application('Reminders');
let lists = Rem.lists();
const wanted = %s;
if (wanted) lists = lists.filter(list => String(list.name()) === wanted);
const includeCompleted = %s;
const out=[];
lists.forEach(list => list.reminders().forEach(reminder => { const completed=reminder.completed(); if (!includeCompleted && completed) return; out.push({name:String(reminder.name()),list:String(list.name()),completed,dueDate:reminder.dueDate()?reminder.dueDate().toISOString():null,priority:reminder.priority(),notes:String(reminder.body()||'')}); }));
return JSON.stringify(out);`, jxaLiteral(optionalString(args, "list")), jxaLiteral(boolArg(args, "includeCompleted", false)))
	return runManagedJXA(ctx, body, "Reminders")
}

func remindersCreate(ctx context.Context, args map[string]any) (any, error) {
	title, err := requiredString(args, "title")
	if err != nil {
		return nil, err
	}
	body := fmt.Sprintf(`
const Rem = Application('Reminders');
let target = null;
const wanted = %s;
if (wanted) { const matches=Rem.lists().filter(list=>String(list.name())===wanted); if (!matches.length) return JSON.stringify({ok:false,error:'list not found'}); target=matches[0]; }
if (!target) { try { target=Rem.defaultList(); } catch(e) { target=Rem.lists()[0]; } }
const props={name:%s};
if (%s) props.body=%s;
if (%s) props.dueDate=new Date(%s);
const priority=%d; if (priority >= 0) props.priority=priority;
const reminder=Rem.Reminder(props); target.reminders.push(reminder);
return JSON.stringify({ok:true,name:String(reminder.name()),list:String(target.name())});`, jxaLiteral(optionalString(args, "list")), jxaLiteral(title), jxaLiteral(optionalString(args, "notes") != ""), jxaLiteral(optionalString(args, "notes")), jxaLiteral(optionalString(args, "dueDate") != ""), jxaLiteral(optionalString(args, "dueDate")), intArg(args, "priority", -1))
	return runManagedJXA(ctx, body, "Reminders")
}

func remindersComplete(ctx context.Context, args map[string]any) (any, error) {
	name, err := requiredString(args, "name")
	if err != nil {
		return nil, err
	}
	body := fmt.Sprintf(`
const Rem=Application('Reminders'); let lists=Rem.lists(); const wantedList=%s; if(wantedList) lists=lists.filter(list=>String(list.name())===wantedList); const wantedName=%s; const matches=[]; lists.forEach(list=>list.reminders().forEach(reminder=>{if(String(reminder.name())===wantedName&&!reminder.completed())matches.push({reminder,list:String(list.name())});})); if(!matches.length)return JSON.stringify({ok:false,error:'reminder not found'}); if(matches.length>1)return JSON.stringify({ok:false,error:'multiple matches; pass list',matches:matches.map(m=>({name:wantedName,list:m.list}))}); matches[0].reminder.completed=true; return JSON.stringify({ok:true,name:wantedName,list:matches[0].list});`, jxaLiteral(optionalString(args, "list")), jxaLiteral(name))
	return runManagedJXA(ctx, body, "Reminders")
}

func macNotify(ctx context.Context, args map[string]any) (any, error) {
	message, err := requiredString(args, "message")
	if err != nil {
		return nil, err
	}
	body := fmt.Sprintf(`const app=Application.currentApplication(); app.includeStandardAdditions=true; const options={}; if(%s)options.withTitle=%s; if(%s)options.subtitle=%s; if(%s)options.soundName=%s; app.displayNotification(%s,options); return JSON.stringify({ok:true});`, jxaLiteral(optionalString(args, "title") != ""), jxaLiteral(optionalString(args, "title")), jxaLiteral(optionalString(args, "subtitle") != ""), jxaLiteral(optionalString(args, "subtitle")), jxaLiteral(optionalString(args, "sound") != ""), jxaLiteral(optionalString(args, "sound")), jxaLiteral(message))
	return runManagedJXA(ctx, body)
}

func textToNotesHTML(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	var builder strings.Builder
	for _, line := range lines {
		builder.WriteString("<div>")
		if line == "" {
			builder.WriteString("<br>")
		} else {
			builder.WriteString(htmlpkg.EscapeString(line))
		}
		builder.WriteString("</div>")
	}
	return builder.String()
}

func jxaLiteral(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func runManagedJXA(ctx context.Context, body string, apps ...string) (any, error) {
	if runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("mac-control is available only on macOS")
	}
	wasRunning := map[string]bool{}
	for _, app := range apps {
		running, err := isMacAppRunning(ctx, app)
		if err != nil {
			// On uncertainty, preserve the app rather than risk closing one the user opened.
			wasRunning[app] = true
			continue
		}
		wasRunning[app] = running
	}

	source := "function run() { try {\n" + body + "\n} catch (e) { return JSON.stringify({__exception:String(e)}); } }"
	result, runErr := runJXASource(ctx, source)
	warnings := []string{}
	for _, app := range apps {
		if wasRunning[app] {
			continue
		}
		if err := quitMacApp(ctx, app); err != nil {
			warnings = append(warnings, err.Error())
		}
	}
	if runErr != nil {
		return nil, runErr
	}
	if object, ok := result.(map[string]any); ok {
		if exception, _ := object["__exception"].(string); exception != "" {
			return nil, fmt.Errorf("macOS automation failed: %s", exception)
		}
		if len(warnings) > 0 {
			object["cleanupWarnings"] = warnings
		}
		return object, nil
	}
	if len(warnings) > 0 {
		return map[string]any{"items": result, "cleanupWarnings": warnings}, nil
	}
	return result, nil
}

func runJXASource(ctx context.Context, source string) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, macControlTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-e", source)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("macOS automation timed out after %s", macControlTimeout)
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("osascript failed: %s", message)
	}
	raw := strings.TrimSpace(stdout.String())
	if raw == "" {
		return nil, fmt.Errorf("osascript returned no output")
	}
	var result any
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, fmt.Errorf("invalid JSON from osascript: %s", truncateString(raw, 300))
	}
	return result, nil
}

func isMacAppRunning(ctx context.Context, app string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-e", fmt.Sprintf("Application(%s).running()", jxaLiteral(app)))
	output, err := command.Output()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(output)) == "true", nil
}

func quitMacApp(ctx context.Context, app string) error {
	quitCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	_ = exec.CommandContext(quitCtx, "/usr/bin/osascript", "-l", "JavaScript", "-e", fmt.Sprintf("Application(%s).quit()", jxaLiteral(app))).Run()
	for i := 0; i < 20; i++ {
		running, err := isMacAppRunning(quitCtx, app)
		if err == nil && !running {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	appleScript := fmt.Sprintf("tell application %s to quit", jxaLiteral(app))
	_ = exec.CommandContext(quitCtx, "/usr/bin/osascript", "-e", appleScript).Run()
	for i := 0; i < 20; i++ {
		running, err := isMacAppRunning(quitCtx, app)
		if err == nil && !running {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("could not close %s after the tool opened it", app)
}
