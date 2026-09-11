---
title: "A project says what it is about"
status: built
---

# 038 — A project says what it is about

**Status:** built. Six of the seven criteria hold as written; the seventh reads differently in practice than it did on paper.

Criterion 3 turned out to be about hiding rather than redaction: a stranger reading a *private* project gets a 404, not a project with its description stripped, because `mayViewProject` hides the whole thing. The description therefore needs no redaction rule of its own; what `Redacted()` had to be checked for is the *public* case, where the description must survive while the owner is still stripped. The test asserts both.

`PATCH /projects/{id}` now takes two optional fields rather than one required one, with pointers so "not sent" and "cleared" stay different; clearing a description is an edit an owner may make.

**Scope:** a project gains a description: a paragraph its owner writes, shown under the title on the project screen and on the registry card, and readable by whoever may read the project. Backend and frontend. One migration.

**Depends on:** [018](018-one-list-of-projects.md) (the list this description also reaches), [006j](006j-the-new-project-flow.md) (where a project is named), [028](028-the-registry-opens.md) (the public card this also feeds).

## Problem

A project has a title and nothing else to read. The title is either what somebody typed or, when they typed nothing, the first paper's reference, so about half of the projects in the database are named after a `doi:` string, and none of them can say why they exist.

That is a gap for three readers at once. The owner returning after a month has the papers to go on and nothing about their own intent. A collaborator invited into a project sees a container and has to ask what it is for. And a stranger reaching a public project through the registry ([028](028-the-registry-opens.md)), the reader this product is ultimately addressed to, gets a title, a verdict and a repository, with no statement of what was being asked.

## Decisions

**It is prose the owner owns.** The field is plain text, and what goes in it is written by a person. [039](039-a-description-can-be-drafted.md) adds a way to draft one with a model, and even there the draft lands in the editor for a person to accept; the stored value is always something somebody chose to keep. Nothing writes this field on its own.

**Its visibility is the project's.** A private project's description is readable by its members; a public project's is readable by anyone, and travels into the registry card and the public project page with the rest of what going public discloses ([006n](006n-who-signed-off.md)). `Redacted()` is where this is decided, so it cannot be answered differently by a second read path.

**Capped, and one paragraph.** Two thousand characters, no markup. It sits under a title in a header and inside a card in a list, and both are places where a long field is a layout problem rather than a feature. A project that needs a document has a repository.

**Edited where it is read.** The description is edited in place on the project screen, the way the title already is ([006j](006j-the-new-project-flow.md)'s inline rename), rather than behind a settings screen; a project has one screen and the thing being described is on it. `PATCH /projects/{id}` grows the field; it is the route that already renames.

**Empty is a state, not a placeholder.** A project with no description shows the owner an invitation to add one, and shows everybody else nothing at all. A stranger should not read "no description yet" on a published project; they should read what is there.

## Acceptance criteria

1. `PATCH /projects/{id}` accepts a description, refuses one over the cap, and refuses a non-member.
2. `GET /projects/{id}` returns the description to a member of a private project, and to anyone for a public one.
3. A stranger reading a private project gets no description, by the same redaction that hides the rest.
4. The project screen shows the description under the title, and lets its owner edit it in place.
5. A project with no description shows its owner an invitation to write one, and shows a reader nothing.
6. The registry card shows a public project's description, trimmed to its line, and a project without one keeps the layout it has today.
7. The description survives a project going public, being withdrawn, being trashed and being restored; it is a field, not part of the sign-off.

## Out of scope

Markup, links and images in the description. Rendering user prose is a decision with a security question attached, and a paragraph does not need it.

A per-paper note. Papers get their bibliography in [037](037-a-paper-carries-its-bibliography.md); a place for a person's own comment on a paper is a separate thing to want, and nobody has asked for it yet.
