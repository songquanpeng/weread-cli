# 本地数据与分析

通过 `weread-cli status --json` 获取数据库的绝对路径，推荐用只读模式打开。先运行 `weread-cli sync` 获取最新数据。表结构版本使用 `PRAGMA user_version`，当前为 1。

| 表 | 内容 |
| --- | --- |
| `books` | `id`、`kind`、`title`、`author`、`in_shelf`、`has_notebook` 和 `data` JSON |
| `notes` | 稳定 `id`、`book_id`、`kind`、`created_unix`、`search_text` 和 `data` JSON |
| `notes_fts` | SQLite FTS5 trigram 索引，由触发器维护 |
| `snapshots` | `key`、`api`、`params` JSON、`data` JSON、`synced_at`，保存最近成功的对应请求 |
| `state` | `last_sync` 保存最近完整成功同步的统计信息 |

`books.kind` 为 `book`、`album`、`mp`。普通电子书 ID 沿用 `bookId`，专辑使用 `album:<albumId>`，文章收藏入口使用 `mp:<bookId>`。`notes.kind` 为 `highlight` 或 `review`。`data` 是 CLI 稳定模型，`raw` 是接口原始字段；数据库保留 raw，普通 CLI 输出默认隐藏它。

`created_unix` 是秒级 Unix 时间，用于排序与日期过滤；JSON 的 `created_at` 是带时区的 RFC3339 时间。同步时间 `synced_at` 使用 UTC RFC3339。`books.data.note_count` 是远端概览中「划线 + 想法/点评 + 书签」之和，并非本地可导出的正文数量。判断正文实际条数请查询 `notes`。

按书统计本地可导出笔记：

```sql
SELECT b.id, b.title,
       SUM(n.kind = 'highlight') AS highlights,
       SUM(n.kind = 'review') AS reviews,
       json_extract(b.data, '$.bookmark_count') AS bookmarks
FROM books b JOIN notes n ON n.book_id = b.id
GROUP BY b.id
ORDER BY COUNT(*) DESC;
```

按创建月份统计想法：

```sql
SELECT substr(json_extract(data, '$.created_at'), 1, 7) AS month,
       COUNT(*) AS reviews
FROM notes WHERE kind = 'review'
GROUP BY month ORDER BY month;
```

年度阅读总时长（必须使用 `totalReadTime`，单位秒）：

```sql
SELECT strftime('%Y', json_extract(params, '$.baseTime'), 'unixepoch', '+8 hours') AS year,
       json_extract(data, '$.totalReadTime') AS seconds,
       ROUND(json_extract(data, '$.totalReadTime') / 3600.0, 2) AS hours,
       json_extract(data, '$.readDays') AS reading_days
FROM snapshots
WHERE api = '/readdata/detail'
  AND json_extract(params, '$.mode') = 'annually'
ORDER BY year;
```

年度返回 `dailyReadTimes` 时，可展开每日时长；缺失代表该周期没有提供明细，不应当解释为零：

```sql
SELECT date(CAST(d.key AS INTEGER), 'unixepoch', '+8 hours') AS day,
       d.value AS seconds
FROM snapshots s, json_each(s.data, '$.dailyReadTimes') d
WHERE s.api = '/readdata/detail'
  AND json_extract(s.params, '$.mode') = 'annually'
ORDER BY day;
```

`readTimes` 的分桶含义随统计周期变化：周/月通常按天，年度按月，总计按年。不要将不同模式直接相加。`dayAverageReadTime` 使用自然日作分母，非阅读日；`preferTime` 的 24 个值从早上 6 点开始排列。跨时区分析请按自己的时区转换，上述 SQL 示例使用 UTC+8。

`snapshots` 是按「接口名 + 排序后的请求 JSON」保存的最新响应，并非历史事件日志；分页请求分别存储，各页可能来自不同同步时间，分析完整个人笔记应使用 `notes` 表。缓存查看命令包含时间戳，读取当前月/周时请留意缓存是否跨期。正常同步只刷新当前周/月；如需历史月/周，请单独运行 `reading --date ... --refresh`。

数据库是真实个人数据镜像，不附带于源码仓库。它不会保存每次修改的历史版本；另存 JSONL/JSON/CSV/Markdown 导出可保留备份。
