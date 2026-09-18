SELECT * FROM people p
JOIN cities c ON c.name = p.city
WHERE jev(p, 'could work from home') AND c.country = 'PT';
