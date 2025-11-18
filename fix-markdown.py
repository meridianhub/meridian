#!/usr/bin/env python3
"""
Automated markdown fixer for common markdownlint issues.
Fixes MD022, MD032, MD031, MD029, and attempts MD013 fixes.
"""

import re
import sys
from pathlib import Path


def fix_blank_lines_around_headings(content):
    """Fix MD022: Ensure blank lines before and after headings."""
    lines = content.split('\n')
    result = []

    for i, line in enumerate(lines):
        # Check if current line is a heading
        is_heading = line.strip().startswith('#') and not line.strip().startswith('####')
        prev_line = lines[i-1] if i > 0 else ''
        next_line = lines[i+1] if i < len(lines) - 1 else ''

        # Add blank line before heading if needed
        if is_heading and i > 0 and prev_line.strip() != '':
            if not result or result[-1].strip() != '':
                result.append('')

        result.append(line)

        # Add blank line after heading if needed
        if is_heading and i < len(lines) - 1 and next_line.strip() != '':
            result.append('')

    return '\n'.join(result)


def fix_blank_lines_around_lists(content):
    """Fix MD032: Ensure blank lines before and after lists."""
    lines = content.split('\n')
    result = []
    in_list = False

    for i, line in enumerate(lines):
        is_list_item = bool(re.match(r'^\s*[-*+]\s', line) or re.match(r'^\s*\d+\.\s', line))
        prev_line = lines[i-1] if i > 0 else ''
        next_line = lines[i+1] if i < len(lines) - 1 else ''

        # Starting a list
        if is_list_item and not in_list:
            if i > 0 and prev_line.strip() != '' and not result[-1].strip() == '':
                result.append('')
            in_list = True

        result.append(line)

        # Ending a list
        if in_list and not is_list_item:
            if line.strip() != '':
                result.insert(-1, '')
            in_list = False

    return '\n'.join(result)


def fix_blank_lines_around_fences(content):
    """Fix MD031: Ensure blank lines before and after code fences."""
    lines = content.split('\n')
    result = []
    in_fence = False

    for i, line in enumerate(lines):
        is_fence = line.strip().startswith('```')
        prev_line = lines[i-1] if i > 0 else ''

        # Starting fence
        if is_fence and not in_fence:
            if i > 0 and prev_line.strip() != '' and (not result or result[-1].strip() != ''):
                result.append('')
            in_fence = True
            result.append(line)
        # Ending fence
        elif is_fence and in_fence:
            result.append(line)
            if i < len(lines) - 1 and lines[i+1].strip() != '':
                result.append('')
            in_fence = False
        else:
            result.append(line)

    return '\n'.join(result)


def fix_ordered_list_numbering(content):
    """Fix MD029: Ensure ordered lists restart at 1 after breaks."""
    lines = content.split('\n')
    result = []
    last_was_ordered = False

    for line in lines:
        match = re.match(r'^(\s*)(\d+)(\.\s+.+)$', line)
        if match:
            indent, num, rest = match.groups()
            if not last_was_ordered:
                # Start of new list, should be 1
                result.append(f'{indent}1{rest}')
            else:
                result.append(line)
            last_was_ordered = True
        else:
            if line.strip() == '':
                last_was_ordered = False
            result.append(line)

    return '\n'.join(result)


def add_language_to_fences(content):
    """Fix MD040: Add language identifier to code fences."""
    lines = content.split('\n')
    result = []

    for i, line in enumerate(lines):
        if line.strip() == '```':
            # Try to infer language from context or default to 'text'
            next_line = lines[i+1] if i < len(lines) - 1 else ''

            # Simple heuristics for language detection
            if 'import' in next_line or 'function' in next_line:
                result.append('```javascript')
            elif 'def ' in next_line or 'class ' in next_line:
                result.append('```python')
            elif '$' in next_line or 'echo' in next_line:
                result.append('```bash')
            elif '{' in next_line or '}' in next_line:
                result.append('```json')
            else:
                result.append('```text')
        else:
            result.append(line)

    return '\n'.join(result)


def wrap_long_lines(content, max_length=120):
    """Attempt to wrap lines exceeding max_length."""
    lines = content.split('\n')
    result = []

    for line in lines:
        # Skip code blocks, URLs, and tables
        if line.strip().startswith('```') or line.strip().startswith('|') or 'http' in line:
            result.append(line)
            continue

        if len(line) > max_length and not line.strip().startswith('#'):
            # Try to wrap at word boundaries
            words = line.split()
            current_line = []
            current_length = 0
            indent = len(line) - len(line.lstrip())
            indent_str = ' ' * indent

            for word in words:
                if current_length + len(word) + 1 > max_length:
                    result.append(indent_str + ' '.join(current_line))
                    current_line = [word]
                    current_length = indent + len(word)
                else:
                    current_line.append(word)
                    current_length += len(word) + 1

            if current_line:
                result.append(indent_str + ' '.join(current_line))
        else:
            result.append(line)

    return '\n'.join(result)


def fix_markdown_file(filepath):
    """Apply all fixes to a markdown file."""
    try:
        content = Path(filepath).read_text(encoding='utf-8')
        original = content

        # Apply fixes in order
        content = fix_blank_lines_around_headings(content)
        content = fix_blank_lines_around_lists(content)
        content = fix_blank_lines_around_fences(content)
        content = fix_ordered_list_numbering(content)
        content = add_language_to_fences(content)
        content = wrap_long_lines(content)

        # Only write if changed
        if content != original:
            Path(filepath).write_text(content, encoding='utf-8')
            return True
        return False
    except Exception as e:
        print(f"Error processing {filepath}: {e}", file=sys.stderr)
        return False


def main():
    """Fix all markdown files in the repository."""
    import subprocess

    # Get list of all markdown files
    result = subprocess.run(
        ['find', '.', '-name', '*.md', '-not', '-path', '*/node_modules/*', '-not', '-path', '*/.git/*'],
        capture_output=True,
        text=True
    )

    files = [f.strip() for f in result.stdout.split('\n') if f.strip()]

    print(f"Found {len(files)} markdown files")
    fixed_count = 0

    for filepath in files:
        if fix_markdown_file(filepath):
            fixed_count += 1
            print(f"Fixed: {filepath}")

    print(f"\nFixed {fixed_count} files")

    # Run linter to check results
    print("\nRunning markdownlint to verify fixes...")
    subprocess.run(['npm', 'run', 'lint:md'])


if __name__ == '__main__':
    main()
