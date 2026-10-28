# pgAdmin

pgAdmin использует email администратора и пароль на порту 80. DATABASE_NETWORK задаёт существующую сеть PostgreSQL. После входа зарегистрируйте сервер вручную; pgAdmin не создаёт PostgreSQL. Данные: /var/lib/pgadmin.

Эта версия поддерживает Docker Compose и службы Swarm. Swarm использует одну реплику на выбранном узле, локальные именованные тома и обновление с предварительной остановкой. Отключение узла данных не вызывает перенос на другой узел.

## Параметры

- `PGADMIN_DEFAULT_EMAIL`
- `PGADMIN_DEFAULT_PASSWORD`
- `DATABASE_NETWORK`

Выберите узел Ready/Active. Постоянные службы работают в одной реплике на этом узле; при его отключении они недоступны.

[Развёртывание Compose и Swarm](swarm-operations.md)
