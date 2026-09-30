// Точка входа веб-интерфейса: монтирует корневой компонент в #app.
import { mount } from "svelte";
import App from "./App.svelte";
import "./app.css";

// Находим контейнер приложения; без него интерфейс не может работать.
const target = document.getElementById("app");
if (!target) {
  throw new Error("mKey: #app container not found");
}

// Монтируем приложение.
mount(App, { target });
