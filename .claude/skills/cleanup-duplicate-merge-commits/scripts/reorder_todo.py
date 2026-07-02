#!/usr/bin/env python3
"""
Rewrite a git rebase -i todo file:
- Keep the first occurrence of each commit subject as 'pick'.
- Move every subsequent occurrence of the same subject to immediately
  follow the first occurrence, as 'fixup'.
- Preserve relative order otherwise.

Usage:
    GIT_SEQUENCE_EDITOR="python3 /abs/path/reorder_todo.py" \
        git rebase -i <base-commit>
"""
import sys
import re
from collections import OrderedDict

path = sys.argv[1]
with open(path) as f:
    lines = f.readlines()

pick_re = re.compile(r'^(pick)\s+([0-9a-f]+)\s+(.*)$')

parsed = []
for line in lines:
    m = pick_re.match(line)
    if m:
        parsed.append({
            'kind': 'pick',
            'action': m.group(1),
            'sha': m.group(2),
            'subject': m.group(3).rstrip(),
            'raw': line.rstrip('\n'),
        })
    else:
        parsed.append({'kind': 'other', 'raw': line.rstrip('\n')})

# Index pick entries by subject in order of appearance.
subject_to_indices = OrderedDict()
for i, p in enumerate(parsed):
    if p['kind'] == 'pick':
        subject_to_indices.setdefault(p['subject'], []).append(i)

emitted = set()
new_picks = []

for i, p in enumerate(parsed):
    if p['kind'] != 'pick' or i in emitted:
        continue
    subject = p['subject']
    occ = subject_to_indices[subject]
    first = parsed[occ[0]]
    new_picks.append({'action': 'pick', 'sha': first['sha'], 'subject': first['subject']})
    emitted.add(occ[0])
    for j in occ[1:]:
        dup = parsed[j]
        new_picks.append({'action': 'fixup', 'sha': dup['sha'], 'subject': dup['subject']})
        emitted.add(j)

output_lines = [f"{e['action']} {e['sha']} {e['subject']}" for e in new_picks]
for p in parsed:
    if p['kind'] == 'other':
        output_lines.append(p['raw'])

with open(path, 'w') as f:
    f.write('\n'.join(output_lines) + '\n')
