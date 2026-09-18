#!/usr/bin/env python3
"""Generate a demo dataset for the jevql playground: a small online store's
customers, products, orders, reviews, support tickets and staff, with varied
free text worth judging. Deterministic (seeded). Prints SQL to stdout."""
import random, itertools, datetime as dt
random.seed(7)

FIRST = "Ada Ravi Joana Miguel Sofia Hans Lucía Tom Inês Pedro Yuki Carlos Amara Noah Priya Leo Fatima Omar Elena Marco Aisha Ben Chloe Diego Emma Finn Grace Hugo Iris Jonas Kai Lena Mateo Nadia Oscar Petra Quinn Rosa Sam Tara Uma Viktor Wren Ximena Yara Zane Anya Bruno Cléo Dmitri".split()
LAST = "Fernandes Patel Silva Costa Almeida Müller Gómez Baker Rocha Martins Tanaka Ruiz Okafor Levine Nair Rossi Haddad Bakr Novak Conti Rahman Adler Dubois Ortega Berg Larsen Chen Moreau Ito Nguyen Kim Weber Sato Ivanova Reyes Hoffmann Park Ahmed Silva Lindqvist".split()
CITIES = [("Lisbon","PT"),("Porto","PT"),("Madrid","ES"),("Berlin","DE"),("Mumbai","IN"),("Austin","US"),("Toronto","CA"),("Lagos","NG"),("Tokyo","JP"),("Paris","FR"),("Dublin","IE"),("São Paulo","BR"),("Seoul","KR"),("Stockholm","SE"),("Denver","US"),("Manchester","GB")]
PLANS = ["free","starter","pro","enterprise"]

PRODUCTS = [
 ("Cold brew kit","kitchen",39.0,"A slow-drip glass tower for 12-hour cold brew. Includes two filters."),
 ("Desk lamp v2","home",64.0,"Warm-to-cool LED with a knurled dimmer. Weighted base, no wobble."),
 ("Wool socks (3 pack)","apparel",28.0,"Merino blend, reinforced heel. Machine washable."),
 ("Trail running shoes","apparel",129.0,"Grippy lugs, wide toe box, drains fast after creek crossings."),
 ("Standing desk mat","home",59.0,"Contoured foam mat with a raised edge for calf stretches."),
 ("Ceramic pour-over","kitchen",34.0,"Cone dripper with ridged walls for even extraction."),
 ("Noise-cancelling earbuds","electronics",149.0,"Six hours per charge, transparency mode, USB-C case."),
 ("Mechanical keyboard","electronics",119.0,"Hot-swappable switches, PBT caps, no RGB nonsense."),
 ("Cast iron skillet","kitchen",45.0,"Pre-seasoned 10 inch. Heavy in the right way."),
 ("Insulated water bottle","outdoors",32.0,"Keeps ice for a day. Dent-prone if you drop it on rocks."),
 ("Camping stove","outdoors",79.0,"Simmer control that actually simmers. Folds into a fist-sized pouch."),
 ("Linen bedsheets","home",139.0,"Stonewashed, breathable, wrinkles are part of the look."),
 ("Kids' rain jacket","apparel",49.0,"Fully taped seams, reflective piping, grows with adjustable cuffs."),
 ("Bluetooth speaker","electronics",89.0,"Loud enough for a kitchen, not for a party."),
 ("Puzzle: 1000 pieces","toys",22.0,"A map of the Lisbon tram network. Pieces are cut oddly on purpose."),
 ("Air fryer XL","kitchen",109.0,"Two baskets, one timer. Chips come out crisp, wings come out great."),
 ("Yoga mat","outdoors",42.0,"Natural rubber, grippy when sweaty, smells like rubber for a week."),
 ("Smart plug (2 pack)","electronics",24.0,"Works with the usual assistants. Schedules survive Wi-Fi drops."),
 ("Beard trimmer","personal care",55.0,"Forty length settings, one of which is right for you."),
 ("Espresso grinder","kitchen",219.0,"Stepless burr grinder. Quiet, single-dosing, a little messy."),
 ("Toddler balance bike","toys",95.0,"No pedals, no training wheels, no tears after the first day."),
 ("Weighted blanket","home",99.0,"Seven kilos of glass beads. Cold to the touch, then very warm."),
 ("Hiking backpack 35L","outdoors",149.0,"Ventilated back, rain cover, hip belt pockets fit a phone."),
 ("Phone tripod","electronics",29.0,"Bendy legs, cold shoe mount, remote shutter included."),
 ("Sourdough starter kit","kitchen",26.0,"Jar, scale, spatula, and a starter that survived a transatlantic post."),
 ("Running belt","apparel",19.0,"Stretchy pocket for a phone and two gels. No bounce."),
 ("Board game: Quarry","toys",48.0,"Two to five players, forty minutes, meaner than it looks."),
 ("Bike lights set","outdoors",36.0,"USB-C, 600 lumens front, 100 rear, mounts without tools."),
 ("Office chair","home",389.0,"Mesh back, adjustable everything. Assembly takes two people."),
 ("Kettle (gooseneck)","kitchen",69.0,"Temperature presets for tea and pour-over. Slow pour, steady arc."),
]

TICKET_TEMPLATES = [
 ("Charged twice for {product}","My card shows two charges for order {order} on the same day. Please refund one of them, this is the second time this year.", "billing"),
 ("Refund request: {product}","The {product} arrived damaged, the box was crushed. I want a refund, not a replacement.", "billing"),
 ("{product} stopped working","After {days} days the {product} just died. No lights, no sound. I tried turning it off and on again and charging it overnight.", "technical"),
 ("Cannot log in","Password reset emails never arrive. I checked spam. My account email is correct. Locked out for {days} days now.", "technical"),
 ("API returns 500 on /orders","Since this morning POST /v1/orders returns 500. Trace id {trace}. Our checkout is down and customers are complaining.", "technical"),
 ("Enterprise pricing?","We are a {size} person company and want a quote for the enterprise tier, ideally with SSO and invoicing.", "sales"),
 ("Upgrade seats","We need {size} more seats on our current plan. Who do I talk to about volume pricing?", "sales"),
 ("Wrong item delivered","Ordered the {product}, received something else entirely. The packing slip says {product} though.", "shipping"),
 ("Where is my order {order}?","Tracking has said 'label created' for {days} days. Was it ever shipped?", "shipping"),
 ("Delivery left in the rain","The courier left the {product} on the doorstep in a storm. Box is soaked. Contents seem ok but I am not happy.", "shipping"),
 ("Invoice PDF broken","The download link on invoice #{order} gives a 404. I need it for expenses by Friday.", "billing"),
 ("Cancel my subscription","Please cancel. Nothing wrong, we just do not use it anymore. Confirm no further charges.", "billing"),
 ("Feature request: dark mode","Would love a dark mode in the app. Not urgent. Great product otherwise!", "product"),
 ("Webhook retries","Are failed webhooks retried? Ours stopped after one attempt when our server was down for {days} minutes.", "technical"),
 ("Discount for nonprofits","Do you offer nonprofit pricing? We are a registered charity in {country}.", "sales"),
 ("Angry: third time asking","This is the THIRD ticket about the same {product} problem. Nobody replies. I am about to dispute the charge with my bank.", "billing"),
 ("Love the {product}","Not a problem, just wanted to say the {product} is the best thing I bought this year. Tell the team.", "product"),
 ("Data export","GDPR request: please send me everything you hold about my account within 30 days.", "legal"),
 ("Security concern","I got an email that looks like it is from you asking for my password. Is this phishing? Screenshot attached.", "security"),
 ("Shipping to {country}?","Do you ship the {product} to {country}? Checkout says unavailable for my address.", "shipping"),
 ("Kids' size chart","Is the {product} true to size for a 4 year old? The chart is confusing.", "product"),
 ("Late again","Second late delivery in a row. If it happens again I am going elsewhere. Order {order}.", "shipping"),
 ("Accessibility","The app is unusable with a screen reader since the last update. Buttons have no labels.", "technical"),
 ("Bulk order","Can we get 40 {product} for a corporate gift, delivered by the 20th?", "sales"),
]
REVIEW_TEMPLATES = {
 5: ["Exactly as described. {product} is now part of my daily routine.","Bought two, gave one away, the friend now uses it more than me.","Worth every cent. {detail}","Sturdy, well made, arrived early. {detail}","I was skeptical about the {product} but it is genuinely great."],
 4: ["Good, not perfect. {detail} Still recommend.","Solid {product}. Minor gripe: {gripe}.","Works as advertised, packaging was excessive.","Nice build, {gripe}, otherwise happy."],
 3: ["It is fine. {detail} {gripe}.","Average. Does the job, nothing more.","Mixed feelings about the {product}: {gripe}."],
 2: ["Disappointed. {gripe}. Considering returning it.","Not what I expected. {gripe}. Support was slow to respond.","{gripe}. For this price I expected better."],
 1: ["Broke after {days} days. {gripe}. Avoid.","Terrible. {gripe}. Asked for a refund and got silence.","Do not buy the {product}. {gripe}. Absolute waste of money.","Arrived damaged and customer service made it worse."],
}
DETAILS = ["The finish is nicer in person.","Setup took two minutes.","My partner keeps borrowing it.","Survived a week of camping.","Great for small apartments.","The instructions were actually readable.","It replaced three other gadgets.","Kids fight over it."]
GRIPES = ["the cable is too short","it smells of plastic for days","the app is clunky","it is louder than advertised","the colour is off from the photos","the lid does not seal properly","the battery fades fast","the sizing runs small","the stitching came loose","it wobbles on my desk","the manual is only in one language","it scratches easily"]

JOB_TITLES = ["Staff software engineer","Line cook","Nurse","Technical writer","Bus driver","Data analyst","Dentist","Customer support lead","Warehouse operator","Accountant","UX designer","Firefighter","Sales development rep","Product manager","Site reliability engineer","Barista","Electrician","Recruiter","Paralegal","Veterinary nurse","Copywriter","Security guard","Physiotherapist","Machine learning engineer","Delivery driver","Teacher","Architect","Pastry chef","Translator","Support engineer"]
BIOS = ["Works from a laptop wherever there is decent coffee. Remote since {year}.","On the floor every shift; the job does not exist without being there.","Splits the week between the clinic and paperwork at home.","Writes docs, runbooks and the occasional angry Slack message.","Drives the early route, home by two, coaches football on Saturdays.","Lives in dashboards. Would take a pay cut for a four day week.","Runs a small practice with two chairs and a waiting list.","Handles escalations over chat and email, mostly from the kitchen table.","Forklift certified. Loads trucks before the city wakes up.","Closes the books monthly, mostly in spreadsheets, sometimes from a beach.","Figma all day, across two time zones, camera off.","Station 4, second platoon. Twenty-four on, forty-eight off.","Cold calls, warm handoffs, always on a headset.","Owns a roadmap nobody reads and a backlog everybody adds to.","Carries the pager one week in four. Has opinions about YAML.","Latte art competitions on weekends. Knows every regular's order.","Crawlspaces, fuse boxes, and the occasional scary discovery.","Reads two hundred CVs a week and remembers the good ones.","Prepares filings, chases signatures, keeps the lawyers on time.","Holds frightened animals and calmer owners in equal measure."]

def d(days_ago): return (dt.date(2026,9,18) - dt.timedelta(days=days_ago)).isoformat()
def esc(t): return t.replace("'", "''")

out = []
out.append("""-- jevql demo dataset (generated by scripts/seed-demo.py)
DROP TABLE IF EXISTS reviews, tickets, order_items, orders, products, customers, employees CASCADE;
CREATE TABLE customers (id serial PRIMARY KEY, name text NOT NULL, email text NOT NULL, city text, country text, plan text NOT NULL, signed_up date NOT NULL, notes text);
CREATE TABLE products (id serial PRIMARY KEY, name text NOT NULL, category text NOT NULL, price numeric(8,2) NOT NULL, description text NOT NULL, in_stock boolean NOT NULL DEFAULT true);
CREATE TABLE orders (id serial PRIMARY KEY, customer_id int REFERENCES customers, placed_at timestamptz NOT NULL, status text NOT NULL, total numeric(10,2) NOT NULL, shipping_note text);
CREATE TABLE order_items (order_id int REFERENCES orders, product_id int REFERENCES products, qty int NOT NULL, PRIMARY KEY (order_id, product_id));
CREATE TABLE reviews (id serial PRIMARY KEY, product_id int REFERENCES products, customer_id int REFERENCES customers, stars int NOT NULL, title text, body text NOT NULL, created_at timestamptz NOT NULL);
CREATE TABLE tickets (id serial PRIMARY KEY, customer_id int REFERENCES customers, subject text NOT NULL, body text NOT NULL, status text NOT NULL, priority text NOT NULL, created_at timestamptz NOT NULL, expected_team text);
CREATE TABLE employees (id serial PRIMARY KEY, name text NOT NULL, job_title text NOT NULL, department text NOT NULL, city text, country text, bio text, salary numeric(10,2), hired date NOT NULL);
""")

customers = []
for i in range(1, 2001):
    n = f"{random.choice(FIRST)} {random.choice(LAST)}"
    city, country = random.choice(CITIES)
    plan = random.choices(PLANS, weights=[50,30,15,5])[0]
    notes = random.choice(["", "", "", "Asked for invoices in EUR.", "Prefers email over phone.", "Complained about delivery once, resolved.", "VIP: friend of the founder.", "Chargeback in 2025, since cleared.", "Wants to be notified about restocks.", "Do not upsell, has asked twice."])
    customers.append((n, city, country, plan))
    out.append(f"INSERT INTO customers (name,email,city,country,plan,signed_up,notes) VALUES ('{esc(n)}','{n.lower().replace(' ','.').replace('í','i').replace('ç','c').replace('ü','u').replace('é','e').replace('ã','a').replace('ê','e').replace('ó','o').replace('á','a')}{i}@example.com','{esc(city)}','{country}','{plan}','{d(random.randint(1,1200))}','{esc(notes)}');")

for name, cat, price, desc in PRODUCTS:
    out.append(f"INSERT INTO products (name,category,price,description,in_stock) VALUES ('{esc(name)}','{cat}',{price},'{esc(desc)}',{'true' if random.random()>0.1 else 'false'});")

NPROD = len(PRODUCTS)
oid = 0
items_out = []
for i in range(1, 6001):
    cid = random.randint(1, 2000)
    days = random.randint(0, 400)
    status = random.choices(["delivered","shipped","processing","cancelled","returned"], weights=[70,10,8,6,6])[0]
    items = random.sample(range(1, NPROD+1), random.randint(1,3))
    total = sum(PRODUCTS[p-1][2] for p in items)
    note = random.choice(["", "", "", "Leave with neighbour if out.", "Ring the bell twice, dog is deaf.", "Gift, no receipt please.", "Deliver after 6pm only.", "Fragile, previous order arrived crushed."])
    out.append(f"INSERT INTO orders (customer_id,placed_at,status,total,shipping_note) VALUES ({cid},'{d(days)} {random.randint(8,22):02d}:{random.randint(0,59):02d}:00+00','{status}',{total:.2f},'{esc(note)}');")
    oid += 1
    for p in items:
        items_out.append(f"INSERT INTO order_items VALUES ({oid},{p},{random.randint(1,3)});")
out.extend(items_out)

for i in range(1, 3001):
    p = random.randint(1, NPROD)
    stars = random.choices([5,4,3,2,1], weights=[40,25,12,12,11])[0]
    tpl = random.choice(REVIEW_TEMPLATES[stars])
    body = tpl.format(product=PRODUCTS[p-1][0], detail=random.choice(DETAILS), gripe=random.choice(GRIPES), days=random.randint(3,90))
    title = random.choice(["", "", PRODUCTS[p-1][0], "Review", "My honest take", "Would buy again" if stars>=4 else "Not for me", "Meh" if stars==3 else ""])
    out.append(f"INSERT INTO reviews (product_id,customer_id,stars,title,body,created_at) VALUES ({p},{random.randint(1,2000)},{stars},'{esc(title)}','{esc(body)}','{d(random.randint(0,400))} 12:00:00+00');")

for i in range(1, 2501):
    subj, body, team = random.choice(TICKET_TEMPLATES)
    p = random.choice(PRODUCTS)[0]
    ctx = dict(product=p, order=random.randint(1000,7000), days=random.randint(2,45), trace=f"{random.randrange(16**6):06x}", size=random.choice([12,25,40,120,300]), country=random.choice(CITIES)[1])
    status = random.choices(["open","pending","closed"], weights=[35,15,50])[0]
    prio = random.choices(["low","normal","high","urgent"], weights=[30,45,18,7])[0]
    out.append(f"INSERT INTO tickets (customer_id,subject,body,status,priority,created_at,expected_team) VALUES ({random.randint(1,2000)},'{esc(subj.format(**ctx))}','{esc(body.format(**ctx))}','{status}','{prio}','{d(random.randint(0,180))} {random.randint(7,20):02d}:{random.randint(0,59):02d}:00+00','{team}');")

for i in range(1, 601):
    n = f"{random.choice(FIRST)} {random.choice(LAST)}"
    jt = random.choice(JOB_TITLES)
    dept = random.choice(["engineering","support","sales","operations","finance","design","people","legal"])
    city, country = random.choice(CITIES)
    bio = random.choice(BIOS).format(year=random.randint(2015,2024))
    out.append(f"INSERT INTO employees (name,job_title,department,city,country,bio,salary,hired) VALUES ('{esc(n)}','{jt}','{dept}','{esc(city)}','{country}','{esc(bio)}',{random.randint(24,180)*1000},'{d(random.randint(30,3000))}');")

out.append("""CREATE INDEX ON tickets (status, created_at);
CREATE INDEX ON reviews (product_id, stars);
CREATE INDEX ON orders (customer_id, placed_at);
CREATE INDEX ON employees (department);
ANALYZE;""")
import re
def batched(lines, size=500):
    buf, head = [], None
    def flush():
        if buf:
            yield head + " VALUES\n" + ",\n".join(buf) + ";"
    for line in lines:
        m = re.match(r"^(INSERT INTO \S+(?: \([^)]*\))?) VALUES (\(.*\));$", line)
        if m:
            h, v = m.group(1), m.group(2)
            if h != head or len(buf) >= size:
                yield from flush(); buf, head = [], h
            buf.append(v)
        else:
            yield from flush(); buf, head = [], None
            yield line
    yield from flush()
print("\n".join(batched(out)))
