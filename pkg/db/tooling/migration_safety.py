"""Conservative, offline checks for up SQL; unknown operations require exact-file review.
保守的离线 up SQL 检查；未知操作必须按文件摘要明确评审。
"""
import hashlib
import re


def statements(source):
    """Split SQL outside comments, identifiers and literals; reject malformed input.
    在注释、标识符和字面量之外拆分 SQL；拒绝不完整输入。
    """
    parts, tokens = [], []
    i = 0
    while i < len(source):
        if source.startswith('--', i):
            end = source.find('\n', i)
            i = len(source) if end < 0 else end + 1
        elif source.startswith('/*', i):
            depth = 1
            i += 2
            while depth and i < len(source):
                if source.startswith('/*', i):
                    depth += 1
                    i += 2
                elif source.startswith('*/', i):
                    depth -= 1
                    i += 2
                else:
                    i += 1
            if depth:
                raise ValueError('unterminated SQL comment')
        elif source[i] in "'\"":
            quote = source[i]
            i += 1
            while i < len(source):
                if source[i] == '\\' and quote == "'":
                    raise ValueError('backslash SQL literals require explicit normalization before safety checking')
                elif source[i] == quote:
                    i += 1
                    if i < len(source) and source[i] == quote:
                        i += 1
                    else:
                        break
                else:
                    i += 1
            else:
                raise ValueError('unterminated SQL literal or identifier')
            tokens.append('IDENT' if quote == '"' else 'LITERAL')
        elif source[i] == '$' and (match := re.match(r'\$(?:[A-Za-z_][A-Za-z_0-9]*)?\$', source[i:])):
            delimiter = match[0]
            end = source.find(delimiter, i + len(delimiter))
            if end < 0:
                raise ValueError('unterminated dollar-quoted SQL')
            i = end + len(delimiter)
            tokens.append('LITERAL')
        elif source[i] == ';':
            if tokens:
                parts.append(tokens)
            tokens = []
            i += 1
        elif source[i].isspace():
            i += 1
        elif match := re.match(r'[A-Za-z_][A-Za-z_0-9]*', source[i:]):
            tokens.append(match[0].upper())
            i += len(match[0])
        else:
            tokens.append(source[i])
            i += 1
    if tokens:
        parts.append(tokens)
    return parts


def check_sql(source):
    issues = []
    for number, tokens in enumerate(statements(source), 1):
        prefix = tokens[:3]
        # Only additive DDL is automatically accepted; DML and opaque code need review.
        # 仅自动接受增量 DDL；数据修改与不透明代码必须评审。
        safe = (prefix[:2] == ['CREATE', 'TABLE'] or
                prefix[:2] == ['CREATE', 'INDEX'] or
                prefix == ['CREATE', 'UNIQUE', 'INDEX'])
        if prefix[:2] == ['ALTER', 'TABLE']:
            for action in (['ON', 'DELETE', 'SET', 'NULL'], ['ON', 'UPDATE', 'SET', 'NULL']):
                while any(tokens[i:i+4] == action for i in range(len(tokens)-3)):
                    index = next(i for i in range(len(tokens)-3) if tokens[i:i+4] == action)
                    tokens = tokens[:index] + tokens[index+4:]
            action = 2
            if tokens[action:action+2] == ['IF', 'EXISTS']:
                action += 2
            if tokens[action:action+1] == ['ONLY']:
                action += 1
            action += 1  # table identifier / 表标识符
            if tokens[action:action+1] == ['.']:
                action += 2
            if tokens[action:action+1] == ['*']:
                action += 1
            additive = tokens[action:action+1] == ['ADD']
            depth = 0
            for index, token in enumerate(tokens[action:], action):
                if token == '(':
                    depth += 1
                elif token == ')':
                    depth -= 1
                elif token == ',' and depth == 0 and tokens[index+1:index+2] != ['ADD']:
                    additive = False
            safe = additive and not any(word in tokens for word in (
                'DROP', 'RENAME', 'ALTER', 'TYPE', 'SET', 'RESET', 'DISABLE', 'ENABLE',
                'INHERIT', 'ATTACH', 'DETACH', 'OWNER', 'SCHEMA', 'USING',
            ) if word != 'ALTER') and tokens.count('ALTER') == 1
            if 'NOT' in tokens and 'NULL' in tokens:
                safe = False
        if any(word in tokens for word in ('SELECT', 'DO', 'CALL', 'EXECUTE', 'FUNCTION', 'PROCEDURE')):
            safe = False
        if not safe:
            issues.append(f'statement {number}: destructive, data-dependent or unsupported SQL requires review')
    return issues


def lint_files(paths, reviewed=()):
    exemptions = set(reviewed)
    failures = []
    for path in paths:
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        identity = path.name + ':' + digest
        source = path.read_text()
        # Revision bookkeeping is generated independently and must remain exact.
        # 版本记录由工具独立生成，必须精确匹配。
        version = int(path.name.split('_')[0])
        source, count = re.subn(
            rf"INSERT INTO schema_migrations\(version,name\) VALUES \({version},'[a-z][a-z0-9_]*'\);",
            '', source,
        )
        if count != 1:
            failures.append(f'{path.name}: invalid revision bookkeeping')
        issues = check_sql(source)
        if issues and identity not in exemptions:
            failures.extend(f'{path.name}: {issue}; review with --reviewed-up {identity}' for issue in issues)
        if identity in exemptions:
            exemptions.remove(identity)
    if exemptions:
        failures.append('reviewed-up does not match current up SQL: ' + ', '.join(sorted(exemptions)))
    if failures:
        raise ValueError('\n'.join(failures))
