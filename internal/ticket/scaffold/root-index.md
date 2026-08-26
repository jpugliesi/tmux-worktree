---
title: "Tickets"
tags:
  - tickets
  - indexes
created: {{CREATED}}
---

# Tickets

Personal agent tickets. One file for each Ticket. One folder for each Project. Do not edit these views by hand. Bases refreshes them.

```base
filters:
  and:
    - file.inFolder("{{FOLDER}}")
    - file.ext == "md"
    - file.name != "index"
    - file.name != "plan"
    - '!file.inFolder("{{FOLDER}}/templates")'
    - '!file.inFolder("{{FOLDER}}/closed")'
views:
  - type: table
    name: Recent
    limit: 20
    order:
      - file.name
      - title
      - status
      - project
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
      - project
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
      - project
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
