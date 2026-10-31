#!/usr/bin/env python3
"""Nagi schema generation and isolated Atlas verification.
Nagi schema 生成与隔离 Atlas 验证。
"""
import argparse
import hashlib
import json
import shutil
import tempfile
import os
from pathlib import Path
import re
import subprocess
import sys
from migration_safety import lint_files

ROOT = Path(__file__).resolve().parents[1]
DB = ROOT / 'pkg/db'
DIR = DB / 'migrations/nagi'
ATLAS_VERSION = 'v1.3.0'


def run(*args, capture=False):
    return subprocess.run(args, cwd=DB, check=True, text=True,
                          stdout=subprocess.PIPE if capture else None).stdout


def check_group(group):
    if group != 'nagi':
        raise ValueError('unknown database group: ' + group)
    for path in [DB / 'schema/nagi', DB / 'generate.go', DIR, DB / 'atlas.hcl']:
        if not path.exists():
            raise ValueError('missing database group resource: ' + str(path))
    config = (DB / 'atlas.hcl').read_text()
    if 'env "nagi"' not in config or 'ent://schema/nagi' not in config or 'file://migrations/nagi' not in config:
        raise ValueError('database group configuration mismatch')
    if 'exclude = ["schema_migrations"]' not in config:
        raise ValueError('SQL-only exclusions must match the reviewed inventory')
    inventory = json.loads((DB / 'sql-only.json').read_text())
    if inventory['database'] != group or inventory['schema'] != 'public' or inventory['business_table_exclusions'] or [obj['exclude'] for obj in inventory['objects']] != ['schema_migrations']:
        raise ValueError('SQL-only inventory mismatch')
    files = list(DIR.glob('*.sql'))
    versions = set()
    for path in files:
        match = re.fullmatch(r'(\d+)_([a-z0-9_]+)\.(up|down)\.sql', path.name)
        if not match:
            raise ValueError('invalid migration filename: ' + path.name)
        pair = path.with_name(path.name.replace('.up.sql', '.down.sql') if '.up.sql' in path.name else path.name.replace('.down.sql', '.up.sql'))
        if not pair.exists():
            raise ValueError('missing migration pair: ' + path.name)
        if match[3] == 'up':
            if match[1] in versions:
                raise ValueError('duplicate migration version: ' + match[1])
            versions.add(match[1])
            if f'INSERT INTO schema_migrations(version,name) VALUES ({int(match[1])},' not in path.read_text():
                raise ValueError('missing schema revision write: ' + path.name)


def checksums():
    return {path.name: hashlib.sha256(path.read_bytes()).hexdigest()
            for path in sorted(DIR.glob('*.sql'))}


def verify_checksums():
    if json.loads((DIR / 'checksums.json').read_text()) != checksums():
        raise ValueError('SQL checksum mismatch, including manually reviewed down SQL')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['check', 'generate', 'hash', 'validate', 'lint', 'diff'])
    parser.add_argument('--group', default='nagi')
    parser.add_argument('--name')
    parser.add_argument('--reviewed-up', action='append', default=[], help='explicitly reviewed filename:sha256; never applies to changed SQL')
    args = parser.parse_args()
    check_group(args.group)
    if args.command not in ['generate', 'hash']:
        verify_checksums()
    if args.command in ['check', 'lint']:
        lint_files(sorted(DIR.glob('*.up.sql')), args.reviewed_up)
        print('Offline SQL safety checks passed (conservative rules; not Atlas Pro analyzers).')
        return
    atlas = os.environ.get('ATLAS_BIN', 'atlas')
    version = run(atlas, 'version', capture=True)
    if not re.search(r'\b' + re.escape(ATLAS_VERSION) + r'(?:\s|$)', version):
        raise ValueError('Atlas ' + ATLAS_VERSION + ' is required')
    if args.command == 'generate':
        if not args.name or not re.fullmatch(r'[a-z][a-z0-9_]*', args.name):
            raise ValueError('--name must be a lowercase migration name')
        before = set(DIR.glob('*.up.sql'))
        run(atlas, 'migrate', 'diff', args.name, '--env', args.group)
        for up in set(DIR.glob('*.up.sql')) - before:
            version = int(up.name.split('_')[0])
            with up.open('a') as output:
                output.write(f"\nINSERT INTO schema_migrations(version,name) VALUES ({version},'{args.name}');\n")
            down = up.with_name(up.name.replace('.up.sql', '.down.sql'))
            with down.open('a') as output:
                output.write(f'\nDELETE FROM schema_migrations WHERE version={version};\n')
        run(atlas, 'migrate', 'hash', '--env', args.group)
        (DIR / 'checksums.json').write_text(json.dumps(checksums(), indent=2) + '\n')
        latest = max(int(path.name.split('_')[0]) for path in DIR.glob('*.up.sql'))
        revision_file = DB / 'version.go'
        revision_file.write_text(re.sub(r'RequiredVersion int64 = \d+', f'RequiredVersion int64 = {latest}', revision_file.read_text()))
        run('gofmt', '-w', str(revision_file))
        check_group(args.group)
        lint_files(sorted(DIR.glob('*.up.sql')), args.reviewed_up)
    elif args.command == 'diff':
        with tempfile.TemporaryDirectory(prefix='nagi-atlas-') as temporary:
            copied = Path(temporary) / 'migrations'
            shutil.copytree(DIR, copied)
            before = set(copied.glob('*.sql'))
            run(atlas, 'migrate', 'diff', 'verify', '--env', args.group,
                '--dir', 'file://' + str(copied) + '?format=golang-migrate')
            if set(copied.glob('*.sql')) != before:
                raise ValueError('Ent schema differs from the migration chain')
    else:
        run(atlas, 'migrate', args.command, '--env', args.group)
        if args.command == 'hash':
            (DIR / 'checksums.json').write_text(json.dumps(checksums(), indent=2) + '\n')


if __name__ == '__main__':
    try:
        main()
    except (ValueError, subprocess.CalledProcessError) as error:
        print(error, file=sys.stderr)
        sys.exit(1)
