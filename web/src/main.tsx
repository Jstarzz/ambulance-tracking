import React from 'react';
import ReactDOM from 'react-dom/client';
import 'maplibre-gl/dist/maplibre-gl.css';
import './styles.css';
import './refinements.css';
import './routing.css';
import './enrollment.css';
import './maps-native.css';
import App from './App';
import EnrollmentControl from './EnrollmentControl';

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
    <EnrollmentControl />
  </React.StrictMode>
);
