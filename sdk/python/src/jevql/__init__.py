"""Python client for jevql.

Two transports return the same :class:`QueryResult` document:

* :class:`HttpTransport` talks to a running ``jevql serve`` over HTTP.
* :class:`CliTransport` runs the ``jevql`` binary with ``--json-table``.

The convenience entry point is :class:`Jevql`::

    from jevql import Jevql

    with Jevql(url="http://127.0.0.1:7433") as db:
        res = db.query("SELECT name FROM people WHERE jev(people, 'could work from home')")
        for row in res.rows:
            print(row)

    cli = Jevql.cli(database_url="postgres://...", api_key="tsk_...")
    print(cli.query_dicts("SELECT 1 AS one"))
"""

from .client import Jevql
from .errors import JevqlError
from .models import Explain, QueryResult, Stats
from .transport import CliTransport, HttpTransport, Transport

__all__ = [
    "Jevql",
    "JevqlError",
    "QueryResult",
    "Stats",
    "Explain",
    "Transport",
    "HttpTransport",
    "CliTransport",
]

__version__ = "0.1.0"
