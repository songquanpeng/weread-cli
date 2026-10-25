# weread-cli

使用 Go 实现的微信读书命令行工具，参考 [flomo-cli](https://github.com/songquanpeng/flomo-cli) 的本地优先设计。将书架、个人划线、想法/点评与阅读数据同步至 SQLite，支持离线中文搜索、定时同步和流式导出，便于个人分析及 AI Agent 使用。

接口直接沿用 [Tencent/WeChatReading 官方 skill](https://github.com/Tencent/WeChatReading)：通过 `https://i.weread.qq.com/api/agent/gateway` 发起请求，携带 `WEREAD_API_KEY` 和 `skill_version=1.0.5`。不需要抓取 Cookie。此项目为独立社区工具，并非腾讯官方 CLI。

## 安装

Go 1.23 或更新版本：

```sh
go install github.com/songquanpeng/weread-cli/cmd/weread-cli@latest
```

安装/更新使用同一条命令；`weread-cli update` 可查看该命令。将 `$(go env GOPATH)/bin` 加入 `PATH`，或从 [Releases](https://github.com/songquanpeng/weread-cli/releases) 下载对应系统的二进制包。

## 开始使用

运行 `weread-cli login` 即可开始。没有有效登录态时，CLI 会显示 [微信读书 Skills 页面](https://weread.qq.com/r/weread-skills)，提示粘贴 API Key；终端输入不回显，验证成功后才保存。已有登录态会先在线验证，无需重新粘贴。

```sh
# 检查已有登录态，或按引导获取、粘贴并保存 key
weread-cli login

# 安装二进制内嵌的配套 skill，无需 npm 或额外下载
weread-cli skills install

# 检查登录、CLI 最新版本、两处 skill 安装情况及版本匹配
weread-cli status --json

# 也可从密码管理器等通过 stdin 提供 key，避免出现在命令参数中
# your-secret-command | weread-cli login --key-stdin

weread-cli sync --json
weread-cli search "人工智能 写作" --json
weread-cli books --notebooks --json
weread-cli get '<note-id>' --json
weread-cli stats
```

**已保存的 key 优先于 `WEREAD_API_KEY`**；没有保存值时才使用环境变量。只使用环境变量时不必执行 `login`；CLI 不会自行读取 `.zshrc`。环境变量中的有效 key 也可通过 `login` 验证并保存。需要主动更换已有 key 时使用 `login --key-stdin`。鉴权失败会引导重新粘贴；网络故障不会被当作 key 过期。无效的新 key 不会覆盖原配置。

`logout` 删除本机保存的 key，保留离线数据；如仍设置环境变量，之后会回退使用它。登录提示写 stderr，JSON 结果写 stdout，成功结果的 `next.command` 提醒下一步运行同步。

## Agent skill 与环境检查

配套 skill 随 CLI 二进制内嵌，可离线安装到两处：

```sh
weread-cli skills install
weread-cli skills status --json
```

- `~/.agents/skills/weread-cli/SKILL.md`
- `~/.claude/skills/weread-cli/SKILL.md`

重复执行 `skills install`（或别名 `skills update`）会安装与当前 CLI 相配的版本，保留目录内其他文件。skill 的版本跟随 CLI 版本，检查时同时比对实际内容，能发现缺失、旧版或被修改的副本。安装不会修改 Codex/Claude 的其他配置，也不依赖腾讯官方 skill 已安装。

`status` 默认在线验证登录态、查询 GitHub 最新正式 Release，并检查两处本地 skill；`login` 成功时也做 CLI/skill 检查。有问题时 stderr 和 JSON 的 `guidance[].command` 都会输出对应命令，例如：

```sh
go install github.com/songquanpeng/weread-cli/cmd/weread-cli@latest
weread-cli skills install
weread-cli login
weread-cli sync --json
```

需要同时升级 CLI 和 skill 时先更新二进制，再运行 `skills install`。版本检查不发送微信读书 key，每次最多等待 5 秒；GitHub 不可用时 `cli.state` 为 `unknown`，不冒充已是最新版本。CLI 不自动执行升级命令。

完全离线检查使用 `weread-cli status --offline --json`，其中登录态为 `unverified`（仅表示本地存在 key），版本状态为 `offline`，仍检查本地 skill。`status` 保留已有字段，并增加 `authentication`、`cli`、`skills`、`guidance`。

skill 和 `agent-guide` 都要求 Agent 在首次使用或需要最新数据时**先运行 `sync` 再查询**，不把未同步的空索引当成远端没有笔记；离线分析时应告知缓存时间。首次未同步时，`status` 也会输出同步命令。定时任务不能保证刚发生的变化已同步。

## 离线搜索与分页

`search`、`list`、`get`、`books`、`stats`、`export`、`skills` 和 `status --offline` 均不访问网络，也不需要有效 key。

```sh
weread-cli search "深度学习" --limit 50 --json
weread-cli search "思考 写作" --any --json
weread-cli search "读书" --kind review --from 2026-01-01 --to 2026-12-31
weread-cli list --book '<book-id>' --kind highlight --sort oldest --json
weread-cli books "作者或书名" --shelf --json
weread-cli books --kind album --json
```

搜索范围包括划线文本、想法正文、引用原文、书名、作者和章节名。空格分隔的关键词默认 AND，`--any` 改为 OR。至少三字的词使用 FTS5 trigram；单字、双字及混合短词使用字面子串匹配，避免中文分词缺失。`%`、`_`、引号等按字面处理，不会变成 SQL/FTS 语法。`books` 的查询在书名、作者和简介中做子串匹配。

分页 JSON 的稳定字段为 `query`、`total`、`count`、`limit`、`offset`、`has_more`、`items` 和 `next`。有下一页时，直接执行 `next.command`。默认搜索返回 20 条，可用 `--limit 1..1000`、`--offset` 调整。后台同步期间每次查询的总数和本页来自同一个数据库快照；跨多次查询的分页可能遇到数据更新，如需完整一致的语料请使用 `export`。

笔记使用稳定 ID：`highlight:<book-id>:<bookmark-id>` 和 `review:<book-id>:<review-id>`。`--from` / `--to` 是 `--timezone` 下的创建日期，包含首尾两天。默认时区为 `Asia/Shanghai`。

## 导出与本地分析

```sh
weread-cli export --output jsonl > notes.jsonl
weread-cli export --output json > notes.json
weread-cli export --output csv > notes.csv
weread-cli export --book '<book-id>' --output markdown > book-notes.md
weread-cli export "主题" --kind review --output jsonl > selected.jsonl
weread-cli export --entity books --output jsonl > books.jsonl
weread-cli export --entity reading --output jsonl > reading.jsonl
weread-cli export --entity snapshots --output jsonl > snapshots.jsonl
```

JSONL 默认每行一条完整记录，流式读取同一个 SQLite 快照，不受搜索分页上限影响。CSV/Markdown 用于笔记；其他实体支持 JSON/JSONL。笔记与书籍的原始接口字段默认不输出，使用 `--include-raw` 获取；`reading` 与 `snapshots` 导出本身就是原始数据。导出的文件含个人内容，应保存在自己的私有目录。

阅读时长单位均为**秒**，阅读进度为 **0–100 的整数百分比**。统计笔记数包含「划线 + 个人想法/点评 + 书签」，但当前接口**不能导出书签正文**。本工具同步官方 `/user/notebooks` 列出的书籍及对应的划线、个人想法；没有关联书籍的内容不承诺可导出。`remote_note_count` 和 `remote_no_book_review_count` 保留网关原始计数，`exportable_notes` 表示实际本地正文条数，两者可能不同。

`reading` 中包含整体、当周、当月及各年的阅读数据。首次同步根据整体统计里的年度桶补全历史年度，后续刷新当前周期；`sync --full` 同时刷新历史年度。也可以用 `sync --years 2024,2025` 显式补充年度。历史月/周可用 `reading --date ... --refresh` 单独缓存。

数据库路径由 `weread-cli status --json` 的 `database` 字段给出，可以使用 SQLite 的 JSON 函数分析，见 [数据结构与 SQL 示例](docs/data-model.md)。

## 定时同步

保存 key 后安装系统级的**当前用户任务**，默认每小时一次：

```sh
weread-cli login
weread-cli schedule install --interval 1h
weread-cli schedule status --json
weread-cli schedule remove
```

macOS 使用 LaunchAgent，安装后立即运行一次，并按 `StartInterval` 周期运行；当前用户登录后自动恢复，机器休眠时无法执行。Linux 使用 `systemd --user` timer；是否可在退出登录后运行取决于用户服务管理器及 linger 配置。任务绑定当前可执行文件的绝对路径，移动/删除该文件后需重新执行 `schedule install`。定时任务不依赖 `.zshrc`，key 不写入 plist/unit 或命令参数。

macOS 日志在状态目录中的 `sync.log` 和 `sync-error.log`；Linux 查看 `journalctl --user -u <schedule status 中的 label>.service`。系统任务安装失败会返回具体错误，不会假装已启用。

所有平台也支持前台循环，便于容器、Windows 任务计划程序或已有进程管理器调用：

```sh
weread-cli sync --watch --interval 1h --json
```

每轮完成后等待指定间隔；成功结果按 JSONL 写入 stdout，进度和失败信息写入 stderr。遇到必须升级的官方 skill 版本提示会停止。`Ctrl-C` / SIGTERM 可结束运行。

## 同步行为

- 每次遍历完整书架与笔记本分页；笔记本使用 `lastSort`，个人想法使用 `synckey`，业务参数平铺在请求顶层。
- 笔记本排序值、数量或书籍信息变化时重新拉取该书的划线与个人想法。未变化且最近 24 小时同步过的笔记本跳过；至少每 24 小时刷新正文一次，覆盖不改变计数的编辑。`--refresh-after 6h` 可缩短，`--full` 强制全部刷新。
- 默认只为最近阅读的 5 本电子书拉取额外阅读进度，避免大书架产生过多请求；可用 `--progress-limit 0` 关闭。其余笔记本保留概览中的进度。
- 所有请求及分页校验成功后，使用单个 SQLite 事务替换本地镜像。失败、无效回包、游标不前进或中断不会推进同步状态。进程锁避免同一数据库并发同步，退出后系统自动释放锁。
- 完整同步会移除远端已删除的个人笔记；从书架移除的书籍保留本地元数据，但标记为不在书架。需要保留历史版本时请定期另存导出文件。
- 请求按最少 250ms 间隔发送，429/5xx 最多尝试 3 次，退避上限 30 秒，单次网络超时 45 秒。重定向不携带凭据继续请求。

这是个人数据镜像，不是电子书全文下载器。公开点评和热门划线只通过显式远程命令获取，不混入个人笔记。

## 在线能力

以下命令显式联网：

```sh
weread-cli remote search "三体" --json
weread-cli remote search "量子" --scope 12 --count 10 --json
weread-cli remote recommend --json
weread-cli remote recommend --similar '<book-id>' --json
weread-cli remote popular '<book-id>' --json
weread-cli remote reviews '<book-id>' --type 3 --json
weread-cli remote review '<review-id>' --json
```

以下命令默认读缓存，`--refresh` 才会联网更新：

```sh
weread-cli shelf --json
weread-cli book '<book-id>' --json
weread-cli book '<book-id>' --view chapters --refresh --json
weread-cli book '<book-id>' --view progress --refresh --json
weread-cli reading --mode overall --json
weread-cli reading --mode annually --date 2025-01-01 --json
weread-cli reading --mode monthly --date 2025-06-01 --refresh --json
```

`book` 默认返回本地书籍信息；如已单独缓存 `/book/info`，则优先返回完整详情。章节目录需要先显式刷新，进度缓存默认仅覆盖最近几本书。缓存包含 `synced_at`，用于判断新鲜度。

完整接口参数以官方 skill 为准。`api` 提供已实现的只读接口白名单入口，支持章节划线热度、划线下评论等较底层能力：

```sh
weread-cli api /_list --json
weread-cli api /user/notebooks --params '{"count":100}' --json
weread-cli api /book/underlines --params '{"bookId":"<book-id>","chapterUid":1}' --json
```

所有远程原始回包保留接口自身的字段名和数值单位。无内置写入/删除远端笔记或修改书架的命令。

## 配置与安全

| 配置 | 说明 |
| --- | --- |
| `WEREAD_API_KEY` | 未保存 key 时的回退值；已保存的 key 优先 |
| `--home` / `WEREAD_HOME` | 状态目录，默认系统用户配置目录下的 `weread-cli` |
| `--db` / `WEREAD_DB` | 数据库路径，默认状态目录中的 `weread.db` |
| `--timezone` | 日期和阅读周期时区，默认 `Asia/Shanghai` |
| `--json` / `--format json` | 机器可读 JSON；导出格式使用 `--output` |
| `--api-url` | 测试用网关覆盖；要求 HTTPS，只有 loopback 可以用 HTTP |

macOS 默认状态目录是 `~/Library/Application Support/weread-cli`；Linux 通常是 `~/.config/weread-cli`，遵循 `XDG_CONFIG_HOME`。目录设为 `0700`，key 与数据库文件设为 `0600`（Windows 的实际访问控制取决于用户目录 ACL）。key 以本机私有 JSON 文件保存；不保存至 Git、不通过参数传入、不打印原值。接口升级提示会中止当前同步，升级本工具后重试。

## 开发

```sh
make test       # 单元、CLI、协议与并发竞态测试；不需要真实 key
make vet
make build
make install    # 将当前源码安装至 GOBIN / GOPATH/bin
```

架构沿用 flomo-cli 的职责划分：`domain` 定义稳定本地模型；`internal/weread` 隔离内部网关协议与 DTO；`service` 编排分页、增量判断和原子同步；`storage` 管理 SQLite、FTS 和流式导出；`cli` 提供命令；`schedule` 管理 launchd/systemd。合成测试覆盖分页、中文搜索、远端删除、失败回滚、缓存、导出、凭据保护与任务配置，不在 CI 中访问真实账户。

主分支及 PR 运行 CI。推送版本标签会测试并构建 macOS、Linux、Windows 的 amd64/arm64 发布包，附 SHA-256 校验文件。

接口来源：[Tencent/WeChatReading](https://github.com/Tencent/WeChatReading)，核对版本 `1.0.5`，源码提交 `d98e66ee60a8c7bcd35da549d0643c4470439643`。官方仓库使用 Apache-2.0；本项目 Go 实现使用 [MIT License](LICENSE)。
