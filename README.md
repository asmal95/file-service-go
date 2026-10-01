# file-service-go

Простой сервис хранения файлов. Загрузка/удаление — по API-ключу,
скачивание и метаданные — публичные (ссылки шарибельные).

## API

| Метод | Путь | Auth | Описание |
|---|---|---|---|
| `GET` | `/healthz` | — | `{"status":"ok","files":N,"bytes_used":N,"bytes_quota":N}` |
| `POST` | `/file/upload` | API-key | multipart: поле `file` + опционально `description` → `201` + JSON метаданных |
| `GET` | `/file/{id}/info` | — | JSON `{id,name,type,size,description,url}` |
| `GET` | `/file/{id}/download` | — | байты файла (стриминг, Range поддерживается) |
| `DELETE` | `/file/{id}` | API-key | `204` или `404` |

Ключ передаётся в `X-API-Key: <key>` или `Authorization: Bearer <key>`.
Ошибки — JSON `{"error":"..."}`.

```bash
curl -H "X-API-Key: $KEY" -F "file=@photo.jpg" -F "description=отпуск" \
  https://files.example.com/file/upload
curl https://files.example.com/file/<id>/info
curl -O https://files.example.com/file/<id>/download
curl -X DELETE -H "X-API-Key: $KEY" https://files.example.com/file/<id>
```

## Конфиг (env)

| Переменная | Дефолт | Описание |
|---|---|---|
| `PORT` | `8080` | порт |
| `FILES_DIRECTORY` | `./data` (в образе `/data`) | каталог хранения |
| `API_KEYS` | — | ключи через запятую; пусто = upload/delete выключены |
| `MAX_FILE_SIZE` | `10MB` | лимит одного файла (`B/KB/MB/GB`, можно `1.5GB`) |
| `MAX_STORAGE_SIZE` | `1GB` | квота хранилища; при переполнении удаляются самые старые |
| `SERVER_URL` | — | база для `url` в `/info`; если пусто — собирается из Host запроса |

## Запуск

```bash
# локально (нужен Go 1.24+)
API_KEYS=secret1,secret2 FILES_DIRECTORY=./data go run .

# docker
docker build -t fileservice .
# контейнер работает от UID 65532 — bind-mount должен быть ему доступен:
mkdir -p /srv/fileservice && chown 65532:65532 /srv/fileservice
docker run -d --name fileservice -p 127.0.0.1:8050:8080 \
  -v /srv/fileservice:/data -e API_KEYS=secret1,secret2 fileservice
```

## Заметки

- Хранение — файлы на диске + JSON-метаданные `<id>.meta` рядом. Бэкап = скопировать каталог.
- Загрузка стримится во временный файл (в RAM целиком не грузится), коммит — через `rename`.
- При переполнении квоты самые старые файлы удаляются молча; файл больше квоты — `413`.
- За nginx: `client_max_body_size` должен быть ≥ `MAX_FILE_SIZE` + запас на multipart (~1MB).
