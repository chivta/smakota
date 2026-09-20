\getenv calbot_password CALBOT_DB_PASSWORD

select format('create role calbot login password %L', :'calbot_password')
where not exists (select from pg_catalog.pg_roles where rolname = 'calbot')
\gexec

select 'create database calbot owner calbot'
where not exists (select from pg_catalog.pg_database where datname = 'calbot')
\gexec
