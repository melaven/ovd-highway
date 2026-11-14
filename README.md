# OVD HIGHWAY 🛣️

**Zero-Blocking Multi-Protocol Opportunity Ingestion & AI Qualification Engine**

Multi-protocol система для сбора и AI-квалификации лидов из различных источников (Habr Career, Telegram каналы) с glassmorphism UI дашбордом.

## 🎨 UI Dashboard

Современный дашборд с:
- **Glassmorphism** эффектами (матовое стекло)
- **Dusk** цветовой палитрой: градиенты `#8D99AE` → `#23253A`
- **Framer Motion** анимациями (плавное появление карточек, hover эффекты)
- **Real-time** обновлениями через WebSocket

### Запуск UI

```bash
cd ui
npm install
npm run dev    # Development на http://localhost:5173
npm run build  # Production сборка
```

## 🏗️ Архитектура

- **Onramp** — точки входа (Habr, Telegram)
- **Lane** — обработка и AI-квалификация (LLM, дедупликация)
- **Exit** — выходные каналы (Telegram бот)

## 🚀 Быстрый старт

```bash
# Backend
go run cmd/engine/main.go

# Frontend
cd ui && npm run dev
```

Подробнее в [UI README](ui/README.md)
