#!/usr/bin/env python3
"""Load a slice of the Amazon-PQA open dataset (registry.opendata.aws/amazon-pqa,
CDLA Permissive 1.0) into Postgres as three tables:

  listings(asin, category, brand, title, bullets text[], description)
  questions(id, asin, category, text, kind, verdict)      -- kind: yes-no | open-ended
  answers(id, question_id, text)                           -- verdict: yes | no | neutral | null

Usage: DATABASE_URL=... scripts/load-amazon-pqa.py category [category ...]
Category names are the file suffixes, e.g. backpacks, area_rugs, external_hard_drives.
Files are streamed from S3 and loaded with COPY; products are kept once per asin."""
import io, json, os, sys, time, urllib.request
import psycopg

BASE = "https://amazon-pqa.s3.amazonaws.com/amazon_pqa_%s.json"
DDL = """
CREATE TABLE IF NOT EXISTS listings (
  asin text PRIMARY KEY, category text NOT NULL, brand text, title text NOT NULL,
  bullets text[] NOT NULL DEFAULT '{}', description text NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS questions (
  id text PRIMARY KEY, asin text NOT NULL REFERENCES listings, category text NOT NULL,
  text text NOT NULL, kind text NOT NULL, verdict text);
CREATE TABLE IF NOT EXISTS answers (
  id bigserial PRIMARY KEY, question_id text NOT NULL REFERENCES questions, text text NOT NULL);
"""

def pgarr(xs):
    return "{" + ",".join('"' + x.replace("\\", "\\\\").replace('"', '\\"') + '"' for x in xs) + "}"

def main(cats):
    url = os.environ["DATABASE_URL"]
    with psycopg.connect(url) as pg:
        pg.execute(DDL)
        seen_asin = {r[0] for r in pg.execute("SELECT asin FROM listings")}
        seen_q = {r[0] for r in pg.execute("SELECT id FROM questions")}
        pg.commit()
        for cat in cats:
            t0 = time.time(); nq = na = np_ = 0
            label = cat.replace("_", " ").replace("&", "&")
            with urllib.request.urlopen(BASE % cat) as resp, pg.cursor() as cur:
                L, Q, A = [], [], []
                def flush():
                    nonlocal L, Q, A
                    for table, cols, rows in (("listings", "asin,category,brand,title,bullets,description", L),
                                              ("questions", "id,asin,category,text,kind,verdict", Q),
                                              ("answers", "question_id,text", A)):
                        if rows:
                            with cur.copy(f"COPY {table} ({cols}) FROM STDIN") as cp:
                                for r in rows:
                                    cp.write_row(r)
                    L, Q, A = [], [], []
                for line in io.TextIOWrapper(resp, encoding="utf-8"):
                    d = json.loads(line)
                    qid, asin = d["question_id"], d["asin"]
                    if qid in seen_q or not asin:
                        continue
                    if asin not in seen_asin:
                        seen_asin.add(asin)
                        bullets = [d.get(f"bullet_point{i}") or "" for i in range(1, 6)]
                        L.append((asin, label, d.get("brand_name") or None, d.get("item_name") or "(untitled)",
                                  pgarr([b for b in bullets if b]), d.get("product_description") or ""))
                        np_ += 1
                    seen_q.add(qid)
                    kind = "yes-no" if (d.get("question_type") == "yes-no" or d.get("is_yes-no_question")) else "open-ended"
                    verdict = d.get("answer_aggregated") or d.get("yes-no_answer")
                    if verdict not in ("yes", "no", "neutral"):
                        verdict = None
                    Q.append((qid, asin, label, d["question_text"], kind, verdict)); nq += 1
                    for a in d.get("answers") or []:
                        if a.get("answer_text"):
                            A.append((qid, a["answer_text"])); na += 1
                    if len(Q) >= 5000:
                        flush()
                flush()
            pg.commit()
            print(f"{cat}: {np_} listings, {nq} questions, {na} answers in {time.time()-t0:.0f}s", flush=True)
        pg.execute("CREATE INDEX IF NOT EXISTS questions_asin ON questions (asin)")
        pg.execute("CREATE INDEX IF NOT EXISTS questions_category ON questions (category)")
        pg.execute("CREATE INDEX IF NOT EXISTS answers_question ON answers (question_id)")
        pg.execute("CREATE INDEX IF NOT EXISTS listings_category ON listings (category)")
        pg.commit()
        print(pg.execute("SELECT (SELECT count(*) FROM listings), (SELECT count(*) FROM questions), (SELECT count(*) FROM answers), pg_size_pretty(pg_database_size(current_database()))").fetchone())

if __name__ == "__main__":
    main(sys.argv[1:])
