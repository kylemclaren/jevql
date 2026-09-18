"""Python client for jevql: semantic SQL (``jev()``) on vanilla Postgres.

``Jevql()`` starts a private embedded engine from the binary bundled with this
package; ``Jevql(url=...)`` talks to a shared ``jevql serve``::

    from jevql import Jevql

    with Jevql() as db:
        for row in db.query_dicts("SELECT name FROM people WHERE jev(people, 'could work from home')"):
            print(row["name"])
"""

from .client import Jevql
from .engine import Engine, resolve_engine
from .errors import JevqlError
from .models import Explain, QueryResult, Stats
from .transport import EmbeddedTransport, HttpTransport, Transport

__all__ = [
    "Jevql",
    "JevqlError",
    "QueryResult",
    "Stats",
    "Explain",
    "Transport",
    "HttpTransport",
    "EmbeddedTransport",
    "Engine",
    "resolve_engine",
]

__version__ = "0.2.0"
