import json

import pytest

from jevql import CliTransport, Jevql, JevqlError
from tests.conftest import SAMPLE


def test_cli_query_and_argv(fake_jevql):
    db = Jevql.cli(binary=fake_jevql.path, database_url="postgres://u:p@h/db", api_key="tsk_1", api_url="https://x/v1")
    res = db.query("SELECT 1", threshold=0.8, max_rows=5)
    assert res.columns == ["name", "p"] and res.rows[0] == ["Ada", 0.93]
    assert fake_jevql.argv() == [
        "--json-table",
        "--threshold", "0.8",
        "--max-rows", "5",
        "--api-key", "tsk_1",
        "--api-url", "https://x/v1",
        "-c", "SELECT 1",
        "postgres://u:p@h/db",
    ]


def test_cli_minimal_argv(fake_jevql):
    Jevql.cli(binary=fake_jevql.path).query("SELECT 2")
    assert fake_jevql.argv() == ["--json-table", "-c", "SELECT 2"]


def test_cli_explain_flag(fake_jevql):
    doc = dict(SAMPLE, explain={"collect_sql": "SELECT 1", "rows": 3, "question_list": []})
    fake_jevql.set(stdout=json.dumps(doc) + "\n")
    ex = Jevql.cli(binary=fake_jevql.path).explain("SELECT 1")
    assert "--explain" in fake_jevql.argv() and ex.rows == 3


def test_cli_env_passthrough(fake_jevql, monkeypatch):
    monkeypatch.setenv("DATABASE_URL", "postgres://from-env")
    Jevql.cli(binary=fake_jevql.path, env={"TYPESAFE_API_KEY": "tsk_env", "JEVQL_TEST": "1"}).query("SELECT 1")
    env = fake_jevql.env()
    assert env["DATABASE_URL"] == "postgres://from-env"
    assert env["TYPESAFE_API_KEY"] == "tsk_env" and env["JEVQL_TEST"] == "1"


@pytest.mark.parametrize("exit_code,code", [(1, "sql"), (2, "api"), (3, "internal")])
def test_cli_exit_code_mapping(fake_jevql, exit_code, code):
    fake_jevql.set(exit=exit_code, stderr="ERROR:  relation \"nope\" does not exist\n")
    with pytest.raises(JevqlError) as ei:
        Jevql.cli(binary=fake_jevql.path).query("SELECT * FROM nope")
    assert ei.value.code == code and ei.value.status == exit_code
    assert 'relation "nope" does not exist' in str(ei.value)


def test_cli_multiple_statements_returns_last(fake_jevql):
    first = dict(SAMPLE, tag="SELECT 1")
    fake_jevql.set(stdout=json.dumps(first) + "\n" + json.dumps(SAMPLE) + "\n")
    assert Jevql.cli(binary=fake_jevql.path).query("SELECT 1; SELECT 2").tag == "SELECT 2"


def test_cli_malformed_output(fake_jevql):
    fake_jevql.set(stdout="not json\n")
    with pytest.raises(JevqlError) as ei:
        Jevql.cli(binary=fake_jevql.path).query("SELECT 1")
    assert ei.value.code == "transport"


def test_cli_missing_binary(tmp_path):
    with pytest.raises(JevqlError) as ei:
        Jevql.cli(binary=str(tmp_path / "definitely-not-jevql")).query("SELECT 1")
    assert ei.value.code == "transport" and "brew install" in str(ei.value)


def test_cli_health(fake_jevql, tmp_path):
    h = Jevql.cli(binary=fake_jevql.path).health()
    assert h["ok"] is True and h["version"] == "0.1.0"
    assert Jevql.cli(binary=str(tmp_path / "nope")).health()["ok"] is False


def test_transport_argv_helper():
    t = CliTransport(binary="jevql")
    assert t.argv("SELECT 1", explain=True) == ["jevql", "--json-table", "--explain", "-c", "SELECT 1"]
