-- Sample data for jevpsql tests and demos.
DROP TABLE IF EXISTS people;
DROP TABLE IF EXISTS cities;
DROP TABLE IF EXISTS tickets;

CREATE TABLE cities (
  name    text PRIMARY KEY,
  country text NOT NULL
);

CREATE TABLE people (
  id        serial PRIMARY KEY,
  name      text NOT NULL,
  city      text,
  country   text,
  job_title text,
  bio       text,
  age       int,
  salary    numeric(10,2),
  joined    timestamptz DEFAULT '2024-01-15 09:30:00+00'
);

CREATE TABLE tickets (
  id      serial PRIMARY KEY,
  subject text NOT NULL,
  body    text NOT NULL,
  status  text NOT NULL DEFAULT 'open'
);

INSERT INTO cities (name, country) VALUES
  ('Lisbon', 'PT'), ('Porto', 'PT'), ('Madrid', 'ES'), ('Berlin', 'DE'), ('Mumbai', 'IN'), ('Austin', 'US');

INSERT INTO people (name, city, country, job_title, bio, age, salary) VALUES
  ('Ada Fernandes',   'Lisbon', 'PT', 'Staff software engineer', 'Builds distributed systems in Go. Remote-first since 2019.', 34, 98000),
  ('Ravi Patel',      'Mumbai', 'IN', 'Line cook',               'Runs the grill station at a busy downtown bistro.', 27, 21000),
  ('Joana Silva',     'Porto',  'PT', 'Nurse',                   'Works night shifts in the paediatric ward.', 41, 39000),
  ('Miguel Costa',    'Lisbon', 'PT', 'Technical writer',        'Writes API docs and runbooks; lives on a laptop.', 29, 52000),
  ('Sofia Almeida',   'Porto',  'PT', 'Bus driver',              'Drives the 502 route through the old town.', 50, 28000),
  ('Hans Müller',     'Berlin', 'DE', 'Data analyst',            'SQL, dashboards and the occasional notebook.', 38, 71000),
  ('Lucía Gómez',     'Madrid', 'ES', 'Dentist',                 'Runs a small clinic near Retiro.', 45, 88000),
  ('Tom Baker',       'Austin', 'US', 'Customer support lead',   'Handles escalations over chat and email from home.', 31, 61000),
  ('Inês Rocha',      'Lisbon', 'PT', 'Warehouse operator',      'Forklift certified, loads trucks on the early shift.', 24, 24000),
  ('Pedro Martins',   'Lisbon', 'PT', 'Accountant',              'Closes the books every month, mostly in spreadsheets.', 47, 58000),
  ('Yuki Tanaka',     'Berlin', 'DE', 'UX designer',             'Figma all day; remote across two time zones.', 33, 66000),
  ('Carlos Ruiz',     'Madrid', 'ES', 'Firefighter',             'Station 4, second platoon.', 36, 41000);

INSERT INTO tickets (subject, body, status) VALUES
  ('Charged twice',            'My card was charged twice for the March invoice. Please refund one.', 'open'),
  ('API returns 500',          'POST /v1/orders returns 500 since the deploy this morning. Trace id abc123.', 'open'),
  ('Enterprise pricing?',      'We are a 300 person company and want a quote for the enterprise tier.', 'open'),
  ('Cannot log in',            'Password reset email never arrives. Checked spam.', 'open'),
  ('Upgrade seats',            'We need 25 more seats on our current plan. Who do I talk to?', 'open'),
  ('Invoice PDF broken',       'The download link on invoice #4821 gives a 404.', 'closed'),
  ('Webhook retries',          'Are failed webhooks retried? Ours stopped after one attempt.', 'open'),
  ('Discount for nonprofits',  'Do you offer nonprofit pricing? We are a registered charity.', 'open');
