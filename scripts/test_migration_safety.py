import tempfile
from pathlib import Path
import unittest
from migration_safety import check_sql, lint_files


class MigrationSafetyTests(unittest.TestCase):
    def test_additive_structure_and_fk_actions(self):
        self.assertEqual(check_sql('CREATE TABLE "items" (id bigint PRIMARY KEY); ALTER TABLE "items" ADD CONSTRAINT fk FOREIGN KEY(id) REFERENCES parent(id) ON DELETE SET NULL;'), [])

    def test_destructive_and_data_dependent_sql_rejected(self):
        for sql in ('DROP TABLE items;', 'TRUNCATE items;', 'ALTER TABLE items RENAME COLUMN a TO b;',
                    'ALTER TABLE items ALTER COLUMN a TYPE smallint;',
                    'ALTER TABLE items ADD COLUMN a text NOT NULL;',
                    'ALTER TABLE items ADD a text, VALIDATE CONSTRAINT items_check;',
                    'ALTER TABLE items ADD a text, FORCE ROW LEVEL SECURITY;',
                    'ALTER TABLE items ADD a text NOT NULL;',
                    "ALTER TABLE items ADD COLUMN a text DEFAULT 'x', ADD COLUMN b text NOT NULL;",
                    "ALTER TABLE items ADD COLUMN a text NOT NULL DEFAULT 'x';",
                    'UPDATE items SET a=1;', 'DELETE FROM items;',
                    'DO $$ BEGIN EXECUTE \'DROP TABLE items\'; END $$;',
                    'ALTER TABLE items ADD COLUMN a text, DROP COLUMN b;'):
            with self.subTest(sql=sql):
                self.assertTrue(check_sql(sql))

    def test_literals_and_nested_comments_do_not_hide_operations(self):
        self.assertEqual(check_sql("/* outer /* nested */ comment */ CREATE TABLE items(a text DEFAULT 'DROP; TABLE');"), [])
        self.assertTrue(check_sql("CREATE TABLE items(a text DEFAULT 'safe;'); /* comment */ DROP TABLE old;"))
        self.assertTrue(check_sql('CREATE TABLE items AS SELECT * FROM old;'))
        for sql in ("SELECT 'unterminated", '/* unfinished', 'DO $body$ unfinished'):
            with self.assertRaises(ValueError):
                check_sql(sql)

    def test_review_is_bound_to_exact_file(self):
        import hashlib
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / '2_remove.up.sql'
            path.write_text("DROP TABLE items; INSERT INTO schema_migrations(version,name) VALUES (2,'remove');")
            with self.assertRaises(ValueError):
                lint_files([path])
            review = path.name + ':' + hashlib.sha256(path.read_bytes()).hexdigest()
            lint_files([path], [review])
            path.write_text(path.read_text() + ' DROP TABLE other;')
            with self.assertRaises(ValueError):
                lint_files([path], [review])


if __name__ == '__main__':
    unittest.main()
