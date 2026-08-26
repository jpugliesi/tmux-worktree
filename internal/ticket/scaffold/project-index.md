---
title: "{{TITLE}}"
tags:
  - tickets
  - indexes
created: {{CREATED}}
---

# {{TITLE}}

Tickets of the {{TITLE}} Project. Do not edit these views by hand. Bases refreshes them.

```base
filters:
  and:
    - file.folder == this.file.folder
    - file.ext == "md"
    - file.name != "index"
    - file.name != "plan"
views:
  - type: table
    name: Recent
    limit: 20
    order:
      - file.name
      - title
      - status
      - created
    sort:
      - property: created
        direction: DESC
  - type: table
    name: Ready
    filters:
      and:
        - 'status == "ready-for-agent"'
        - "!claimed_by"
    order:
      - priority
      - file.name
      - title
      - blocked_by
    sort:
      - property: priority
        direction: ASC
      - property: file.name
        direction: ASC
  - type: table
    name: Blocked
    filters:
      and:
        - blocked_by
        - 'status != "done"'
        - 'status != "wontfix"'
    order:
      - file.name
      - title
      - status
      - blocked_by
  - type: table
    name: Claimed
    filters:
      and:
        - claimed_by
        - 'status != "done"'
        - 'status != "wontfix"'
    order:
      - claimed_by
      - file.name
      - title
      - status
      - claimed_at
```
