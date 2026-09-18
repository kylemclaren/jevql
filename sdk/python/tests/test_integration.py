"""Runs against a real `jevql serve` when JEVQL_SERVE_URL is set."""

import os

import pytest

from jevql import Jevql

URL = os.environ.get("JEVQL_SERVE_URL")


@pytest.mark.skipif(not URL, reason="JEVQL_SERVE_URL not set")
def test_live_passthrough():
    db = Jevql(url=URL, token=os.environ.get("JEVQL_TOKEN"))
    assert db.health()["ok"] is True
    res = db.query("SELECT 1 AS one, 'x' AS s")
    assert res.columns == ["one", "s"] and res.rows == [[1, "x"]]
    assert res.jev is False and res.stats is None


@pytest.mark.skipif(not URL, reason="JEVQL_SERVE_URL not set")
def test_live_explain():
    db = Jevql(url=URL, token=os.environ.get("JEVQL_TOKEN"))
    ex = db.explain("SELECT name FROM people WHERE jev(people, 'could work from home')")
    assert ex.rows >= 0 and "FROM people" in ex.collect_sql
