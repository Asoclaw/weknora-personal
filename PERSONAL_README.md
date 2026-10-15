# WeKnora 个人知识库

这是基于 WeKnora v0.8.2 发布提交 `3e8b0bfc` 的独立个人版源码。保留原有知识库、搜索问答、自动 Wiki、文件和网页导入等能力；个人界面只隐藏组织成员协作管理入口，个人功能配置入口开放。新增腾讯文档与摹客数据源连接器，复用原有同步调度、日志和删除开关。

个人版仓库：[Asoclaw/weknora-personal](https://github.com/Asoclaw/weknora-personal)。

## 当前个人版能力（2026-10-15）

- 腾讯文档支持文件夹／单文件同步，优先取得原格式文件；无法下载但能读取有效正文时使用Markdown兜底，保留真实原文链接。修复图片型PPT的包内图片提取。
- 摹客支持通用连接参数、登录后发现团队、多选项目、递归画板分组；优先同步画板图片，有有效文字时可使用Markdown兜底。
- 文件浏览支持数据源分组、直接下级文件夹、累计目录计数、可调整目录宽度、图片缩略图与连续预览，以及具体扩展名多选筛选。
- 内容未变的同步保留资料ID、片段、Wiki及关联，补链接和标题／目录变化只更新必要元数据；不因新增功能自动全库重建。
- 解析采用固定并发与内存保护、DNS有界重试、Excel样式兼容及向量分批；图片完成计数幂等，未完成表格可从既有片段继续。`cmd/parse-recovery`是管理员恢复工具，使用前须备份并核对范围，不自动运行。

## 构建与运行

- 前端在 `frontend/` 中以 `VITE_PERSONAL_MODE=true` 执行现有构建命令，产物 `frontend/dist/` 用 `personal/Dockerfile.personal-ui` 打包为 `weknora-personal-ui:v0.8.2`。
- 后端使用 Go 1.26 和 Rust，先运行 `scripts/build-anydoc-lib.sh` 构建上游锁定的 anydoc 静态库，再从 `./cmd/server` 以 `-tags anydoc` 编译为根目录的 `WeKnora-personal`，并注入 `internal/handler.Version=v0.8.2`，使用 `personal/Dockerfile.personal-app` 打包为 `weknora-personal-app:v0.8.2`。Windows 主机建议使用 `personal/Dockerfile.go-builder` 提供的 Linux 构建环境。
- 以原项目 `docker-compose.yml` 加载 `personal/compose.personal.yml`，只启动 `postgres`、`redis`、`docreader`、`app`、`frontend` 五个服务。具体端口、模型和密钥通过本地 `.env` 配置；本仓库只保留 `.env.example`。
- DocReader需使用`personal/Dockerfile.personal-docreader`构建个人镜像，才能包含当前PPT图片读取修复；前端、后端与DocReader的文件上限一致为60MB。
- `personal/backup-weknora.ps1` 是 Windows Docker 部署的备份脚本，备份保存在本地 `data/backups/`，不会纳入 Git。

构建后，在仓库根目录执行（后端二进制和前端产物须先按上述步骤生成）：

```sh
docker build -f personal/Dockerfile.personal-app -t weknora-personal-app:v0.8.2 .
docker build -f personal/Dockerfile.personal-ui -t weknora-personal-ui:v0.8.2 frontend
docker build -f personal/Dockerfile.personal-docreader -t weknora-personal-docreader:v0.8.2 .
docker compose -f docker-compose.yml -f personal/compose.personal.yml up -d postgres redis docreader app frontend
```

运行前复制并填写`.env.example`对应的本地`.env`；服务端口沿用主Compose与本地环境配置，不把开发者本机端口当成部署要求。模型和数据源凭证在原生设置中配置。既有部署更新前先备份数据库、原文件和本地配置，禁止直接清空卷或资料。

当前代码已在隔离测试栈及实际部署完成腾讯文档企业版连接、原格式入库、检索、重复同步、来源链接和图片读取验证；摹客及解析稳定性修复也已完成对应回归。个人版腾讯文档授权须由使用者独立验证，只同步接口能稳定取得的内容。

本仓库不含个人资料、数据库、文件卷、备份、账号凭证、访问令牌和构建产物。上游项目许可证见 [LICENSE](LICENSE)。

默认只启动五个核心服务。可选功能沿用上游配置流程；升级已有数据前须备份数据库、文件及本地配置，并在隔离恢复环境验证迁移。旧镜像须搭配迁移前的数据库快照回退。

anydoc 静态库准备完成后，用 `sh personal/build-backend.sh` 构建后端。该脚本将版本格式与前端统一，并注入当前提交号和构建时间；前端在同一提交下构建。
