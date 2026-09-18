SELECT jev_choice(tickets, 'which team?', ARRAY['billing','technical','sales']) AS team,
       count(*)
FROM tickets
WHERE status = 'open'
GROUP BY 1;
