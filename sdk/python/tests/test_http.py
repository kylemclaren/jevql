import pytest

from jevql import Explain, Jevql, JevqlError, QueryResult, Stats


def test_query_success(server):
    with Jevql(url=server.url) as db:
        res = db.query("SELECT name, jev_prob(people, 'x') AS p FROM people")
    assert isinstance(res, QueryResult)
    assert res.columns == ["name", "p"]
    assert res.rows == [["Ada", 0.93], ["Bo", 0.12]]
    assert res.row_count == 2 and len(res) == 2
    assert res.tag == "SELECT 2" and res.jev is True
    assert isinstance(res.stats, Stats)
    assert res.stats.judged == 2 and res.stats.input_tokens == 400
    assert res.stats.elapsed_ms == pytest.approx(812.5)
    assert res.explain is None
    assert list(res) == res.rows


def test_request_body_and_headers(server):
    server.token = "sekrit"
    db = Jevql(url=server.url + "/", token="sekrit", timeout=5)
    db.query("SELECT 1", threshold=0.7, max_rows=10)
    req = server.requests[-1]
    assert req["method"] == "POST" and req["path"] == "/v1/query"
    assert req["body"] == {"sql": "SELECT 1", "threshold": 0.7, "max_rows": 10}
    assert req["headers"]["Authorization"] == "Bearer sekrit"
    assert req["headers"]["Content-Type"] == "application/json"
    # no optional fields when not given
    db.query("SELECT 2")
    assert server.requests[-1]["body"] == {"sql": "SELECT 2"}


def test_query_dicts(server):
    assert Jevql(url=server.url).query_dicts("SELECT 1") == [
        {"name": "Ada", "p": 0.93},
        {"name": "Bo", "p": 0.12},
    ]


def test_explain(server):
    ex = Jevql(url=server.url).explain("SELECT name FROM people WHERE jev(people, 'x')")
    assert isinstance(ex, Explain)
    assert server.requests[-1]["body"] == {"sql": "SELECT name FROM people WHERE jev(people, 'x')", "explain": True}
    assert ex.rows == 12 and ex.collect_sql == "SELECT name FROM people"
    assert ex.question_list == ['noul "could work from home" on people']
    assert ex.server_order is False


def test_health(server):
    h = Jevql(url=server.url).health()
    assert h["ok"] is True and h["version"] == "0.1.0"
    assert server.requests[-1]["path"] == "/v1/health"


@pytest.mark.parametrize(
    "status,code",
    [(400, "sql"), (401, "auth"), (402, "budget"), (500, "internal"), (502, "api")],
)
def test_error_mapping(server, status, code):
    server.next = ("error", status, code, f"boom {status}")
    with pytest.raises(JevqlError) as ei:
        Jevql(url=server.url).query("SELECT nope")
    assert ei.value.code == code
    assert ei.value.status == status
    assert str(ei.value) == f"boom {status}"


def test_error_status_without_code_falls_back_to_status_map(server):
    server.next = ("raw", 402, b"over budget")
    with pytest.raises(JevqlError) as ei:
        Jevql(url=server.url).query("SELECT 1")
    assert ei.value.code == "budget" and "over budget" in str(ei.value)


def test_unauthorized_when_token_missing(server):
    server.token = "sekrit"
    with pytest.raises(JevqlError) as ei:
        Jevql(url=server.url).query("SELECT 1")
    assert ei.value.code == "auth" and ei.value.status == 401


def test_connection_refused():
    with pytest.raises(JevqlError) as ei:
        Jevql(url="http://127.0.0.1:9", timeout=2).query("SELECT 1")
    assert ei.value.code == "transport"


def test_no_url_means_embedded():
    db = Jevql()
    assert db.embedded and db.transport.engine.process is None  # nothing spawned until first use
    db.close()
