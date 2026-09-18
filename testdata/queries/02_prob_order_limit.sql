SELECT name, jev_prob(people, 'the name is European') AS p
FROM people
WHERE country = 'PT'
ORDER BY p DESC
LIMIT 10;
