import React from 'react';
import ReactDOM from 'react-dom/client';
import 'maplibre-gl/dist/maplibre-gl.css';
import './refinements.css';
import './enrollment.css';
import App from './App';
import EnrollmentControl from './EnrollmentControl';

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
    <EnrollmentControl />
  </React.StrictMode>
);
